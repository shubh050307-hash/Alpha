// Package ipfs provides IPFS integration for Alpha Network.
//
// It wraps the IPFS Kubo HTTP API (typically at localhost:5001) for pinning
// and fetching task result data. When no IPFS node is available, it falls
// back to a local content-addressed store (SHA256 → file) so that agents
// can always submit and retrieve result data.
//
// Operations:
//   - Add(content) → CID (Content IDentifier)
//   - Pin(cid)    → ensure content stays available
//   - Cat(cid)    → retrieve content by CID
//   - Unpin(cid)  → release content (garbage-collectable)
package ipfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client handles IPFS operations, with optional local fallback.
type Client struct {
	apiURL   string // IPFS HTTP API base URL (e.g. http://localhost:5001)
	localDir string // local fallback content-addressed storage directory
	client   *http.Client
}

// NewClient creates an IPFS client. If apiURL is empty, only local storage is used.
// localDir is the directory for local content-addressed fallback storage.
func NewClient(apiURL, localDir string) *Client {
	if localDir == "" {
		localDir = filepath.Join(os.TempDir(), "alpha-ipfs")
	}
	os.MkdirAll(localDir, 0755)
	return &Client{
		apiURL:   apiURL,
		localDir: localDir,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// IsAvailable returns true if the IPFS node is reachable.
func (c *Client) IsAvailable() bool {
	if c.apiURL == "" {
		return false
	}
	resp, err := c.client.Get(c.apiURL + "/api/v0/version")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// AddResult stores task result content and returns its CID.
// If IPFS is available, it's pinned there. Otherwise it uses local SHA256 storage.
func (c *Client) AddResult(agentID, taskID string, data []byte) (cid string, err error) {
	if c.apiURL != "" && c.IsAvailable() {
		return c.addToIPFS(data)
	}
	return c.addLocal(agentID, taskID, data)
}

// Cat retrieves content by CID. Tries IPFS first, then local fallback.
func (c *Client) Cat(cid string) ([]byte, error) {
	if c.apiURL != "" && c.IsAvailable() {
		data, err := c.catFromIPFS(cid)
		if err == nil {
			return data, nil
		}
		log.Printf("[ipfs] IPFS cat failed for %s, trying local: %v", cid, err)
	}
	return c.catLocal(cid)
}

// Pin ensures content identified by CID is pinned locally.
func (c *Client) Pin(cid string) error {
	if c.apiURL != "" && c.IsAvailable() {
		return c.pinToIPFS(cid)
	}
	// Local storage is implicitly pinned (files are not GC'd)
	return nil
}

// Unpin releases a pinned CID.
func (c *Client) Unpin(cid string) error {
	if c.apiURL != "" && c.IsAvailable() {
		return c.unpinFromIPFS(cid)
	}
	// Local: remove the file
	return c.removeLocal(cid)
}

// --- IPFS HTTP API methods ---

func (c *Client) addToIPFS(data []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "result")
	if err != nil {
		return "", fmt.Errorf("create multipart: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("write multipart: %w", err)
	}
	w.Close()

	resp, err := c.client.Post(c.apiURL+"/api/v0/add?pin=true", w.FormDataContentType(), &buf)
	if err != nil {
		return "", fmt.Errorf("ipfs add: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Hash string `json:"Hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode ipfs add response: %w", err)
	}

	log.Printf("[ipfs] added content: %s (%d bytes)", result.Hash, len(data))
	return result.Hash, nil
}

func (c *Client) catFromIPFS(cid string) ([]byte, error) {
	resp, err := c.client.Post(c.apiURL+"/api/v0/cat?arg="+cid, "", nil)
	if err != nil {
		return nil, fmt.Errorf("ipfs cat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ipfs cat: HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) pinToIPFS(cid string) error {
	resp, err := c.client.Post(c.apiURL+"/api/v0/pin/add?arg="+cid, "", nil)
	if err != nil {
		return fmt.Errorf("ipfs pin: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (c *Client) unpinFromIPFS(cid string) error {
	resp, err := c.client.Post(c.apiURL+"/api/v0/pin/rm?arg="+cid, "", nil)
	if err != nil {
		return fmt.Errorf("ipfs unpin: %w", err)
	}
	resp.Body.Close()
	return nil
}

// --- Local content-addressed storage (fallback) ---

// addLocal stores data in a content-addressed way using SHA256.
// The file is saved as localDir/<cid> and the CID is hex-encoded SHA256.
func (c *Client) addLocal(agentID, taskID string, data []byte) (string, error) {
	h := sha256.Sum256(data)
	cid := "local:" + hex.EncodeToString(h[:])

	filePath := filepath.Join(c.localDir, cid)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("write local: %w", err)
	}

	// Also save metadata for discoverability
	meta := map[string]interface{}{
		"agent_id":   agentID,
		"task_id":    taskID,
		"cid":        cid,
		"size":       len(data),
		"stored_at":  time.Now().UTC().Format(time.RFC3339),
	}
	metaJSON, _ := json.Marshal(meta)
	os.WriteFile(filePath+".meta.json", metaJSON, 0644)

	log.Printf("[ipfs:local] stored %s (%d bytes)", cid, len(data))
	return cid, nil
}

func (c *Client) catLocal(cid string) ([]byte, error) {
	filePath := filepath.Join(c.localDir, cid)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("local cat: %w", err)
	}
	return data, nil
}

func (c *Client) removeLocal(cid string) error {
	filePath := filepath.Join(c.localDir, cid)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local remove: %w", err)
	}
	os.Remove(filePath + ".meta.json") // best-effort metadata cleanup
	return nil
}

// --- Helpers ---

// IsLocalCID returns true if the CID was generated by local fallback storage.
func IsLocalCID(cid string) bool {
	return strings.HasPrefix(cid, "local:")
}

// Info returns diagnostic information about the IPFS client.
func (c *Client) Info() map[string]interface{} {
	info := map[string]interface{}{
		"ipfs_api":   c.apiURL,
		"local_dir":  c.localDir,
		"available":  c.IsAvailable(),
	}
	if c.apiURL != "" && c.IsAvailable() {
		info["mode"] = "ipfs"
	} else {
		info["mode"] = "local-fallback"
	}
	return info
}
