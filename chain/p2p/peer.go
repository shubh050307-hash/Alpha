// Package p2p implements basic peer discovery and announcement for Alpha Network.
// Peers communicate via simple HTTP (no custom protocol required at this stage).
// The PeerStore tracks known peers and persists them to a JSON file.
package p2p

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Peer represents a known network peer.
type Peer struct {
	Address   string `json:"address"`
	Port      int    `json:"port"`
	AgentID   string `json:"agent_id,omitempty"`
	LastSeen  int64  `json:"last_seen"`  // Unix timestamp
	LatencyMs int64  `json:"latency_ms"` // round-trip latency in ms; 0 if unknown
}

// URL returns the base HTTP URL for this peer.
func (p *Peer) URL() string {
	return fmt.Sprintf("http://%s:%d", p.Address, p.Port)
}

// PeerStore tracks known network peers with thread-safe access.
type PeerStore struct {
	mu    sync.RWMutex
	peers map[string]*Peer // key: "address:port"
}

// NewPeerStore creates an empty PeerStore.
func NewPeerStore() *PeerStore {
	return &PeerStore{
		peers: make(map[string]*Peer),
	}
}

// key returns the canonical map key for a peer.
func key(address string, port int) string {
	return fmt.Sprintf("%s:%d", address, port)
}

// Add adds or updates a peer in the store.
func (ps *PeerStore) Add(p *Peer) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	k := key(p.Address, p.Port)
	if existing, ok := ps.peers[k]; ok {
		// Update fields but keep latency history
		existing.LastSeen = p.LastSeen
		if p.AgentID != "" {
			existing.AgentID = p.AgentID
		}
		if p.LatencyMs > 0 {
			existing.LatencyMs = p.LatencyMs
		}
	} else {
		ps.peers[k] = p
	}
}

// Remove removes a peer from the store.
func (ps *PeerStore) Remove(address string, port int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.peers, key(address, port))
}

// List returns a snapshot of all known peers.
func (ps *PeerStore) List() []*Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	result := make([]*Peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		cp := *p
		result = append(result, &cp)
	}
	return result
}

// Count returns the number of known peers.
func (ps *PeerStore) Count() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.peers)
}

// Save persists the peer list to a JSON file.
func (ps *PeerStore) Save(path string) error {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]*Peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		cp := *p
		peers = append(peers, &cp)
	}

	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peers: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Load reads the peer list from a JSON file, merging into the current store.
func (ps *PeerStore) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no file yet — that's fine
		}
		return fmt.Errorf("read peers file: %w", err)
	}

	var peers []*Peer
	if err := json.Unmarshal(data, &peers); err != nil {
		return fmt.Errorf("parse peers file: %w", err)
	}

	for _, p := range peers {
		ps.Add(p)
	}
	return nil
}

// Announce sends this node's existence to all known peers.
// myAddr should be "host:port" so peers can call back.
func (ps *PeerStore) Announce(myAddr string, knownPeers []string) {
	// Parse myAddr into address + port
	myHost, myPortStr, err := net.SplitHostPort(myAddr)
	if err != nil {
		log.Printf("[p2p] announce: invalid myAddr %q: %v", myAddr, err)
		return
	}
	var myPort int
	fmt.Sscanf(myPortStr, "%d", &myPort)

	payload := map[string]interface{}{
		"address":  myHost,
		"port":     myPort,
		"agent_id": "",
	}
	body, _ := json.Marshal(payload)

	// Announce to all currently known peers
	all := ps.List()
	for _, p := range all {
		go func(peer *Peer) {
			url := peer.URL() + "/api/v1/peers/announce"
			resp, err := httpPost(url, body)
			if err != nil {
				log.Printf("[p2p] announce to %s failed: %v", peer.URL(), err)
				return
			}
			_ = resp.Body.Close()
		}(p)
	}

	// Also announce to any explicitly-provided peers (seed list)
	for _, addr := range knownPeers {
		go func(a string) {
			url := "http://" + a + "/api/v1/peers/announce"
			resp, err := httpPost(url, body)
			if err != nil {
				log.Printf("[p2p] announce to seed %s failed: %v", a, err)
				return
			}
			_ = resp.Body.Close()
		}(addr)
	}
}

// Bootstrap connects to seed peers and fetches their peer lists, populating
// the local PeerStore. seedPeers should be "host:port" strings.
func (ps *PeerStore) Bootstrap(seedPeers []string) {
	for _, addr := range seedPeers {
		url := "http://" + addr + "/api/v1/peers"
		start := time.Now()
		resp, err := http.Get(url) //nolint:noctx
		if err != nil {
			log.Printf("[p2p] bootstrap: cannot reach seed %s: %v", addr, err)
			continue
		}
		latency := time.Since(start).Milliseconds()

		var result struct {
			Peers []*Peer `json:"peers"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			log.Printf("[p2p] bootstrap: bad response from %s: %v", addr, err)
			_ = resp.Body.Close()
			continue
		}
		_ = resp.Body.Close()

		// Add the seed itself
		host, portStr, err := net.SplitHostPort(addr)
		if err == nil {
			var port int
			fmt.Sscanf(portStr, "%d", &port)
			ps.Add(&Peer{
				Address:   host,
				Port:      port,
				LastSeen:  time.Now().Unix(),
				LatencyMs: latency,
			})
		}

		// Add its peers
		for _, p := range result.Peers {
			p.LastSeen = time.Now().Unix()
			ps.Add(p)
		}
		log.Printf("[p2p] bootstrap: discovered %d peers from %s (latency %dms)",
			len(result.Peers)+1, addr, latency)
	}
}

// httpPost sends a POST request with a JSON body.
func httpPost(url string, body []byte) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Post(url, "application/json", bytes.NewReader(body))
}
