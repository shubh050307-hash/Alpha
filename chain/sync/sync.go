// Package sync implements block synchronisation for Alpha Network.
// A new node can use the Syncer to catch up with a trusted peer by fetching
// blocks one by one, validating each hash before persisting.
package sync

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/producer"
	"github.com/alpha-network/alpha/chain/store"
)

// Syncer synchronises the local chain against a remote peer.
type Syncer struct {
	client *http.Client
}

// NewSyncer creates a Syncer with a reasonable default HTTP timeout.
func NewSyncer() *Syncer {
	return &Syncer{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// blockResponse wraps the /api/v1/blocks/{height} JSON envelope.
type blockResponse struct {
	Success bool        `json:"success"`
	Block   *core.Block `json:"block"`
	Error   string      `json:"error,omitempty"`
}

// latestResponse wraps the /api/v1/blocks/latest JSON envelope.
type latestResponse struct {
	Success bool        `json:"success"`
	Block   *core.Block `json:"block"`
	Error   string      `json:"error,omitempty"`
}

// SyncFromPeer fetches blocks from peerURL until the local chain matches the
// peer's height. Each block's hash is validated before it is stored.
//
// Parameters:
//   - peerURL: base URL of the peer node, e.g. "http://1.2.3.4:8080"
//   - st:      local BadgerDB store to persist synced blocks
//   - prod:    local BlockProducer (used to read current local height)
func (s *Syncer) SyncFromPeer(peerURL string, st *store.Store, prod *producer.BlockProducer) error {
	if peerURL == "" {
		return fmt.Errorf("peerURL cannot be empty")
	}

	// 1. Fetch peer's latest block to determine their height
	peerLatest, err := s.fetchLatest(peerURL)
	if err != nil {
		return fmt.Errorf("fetch peer latest block: %w", err)
	}
	peerHeight := peerLatest.Height

	// 2. Determine local height
	localHeight := uint64(0)
	if prod != nil {
		localHeight = prod.GetChainHeight()
	}

	if peerHeight <= localHeight {
		log.Printf("[sync] already at or ahead of peer (local=%d, peer=%d)", localHeight, peerHeight)
		return nil
	}

	log.Printf("[sync] syncing blocks %d→%d from %s", localHeight+1, peerHeight, peerURL)

	// 3. Fetch and validate each missing block
	for h := localHeight + 1; h <= peerHeight; h++ {
		block, err := s.fetchBlock(peerURL, h)
		if err != nil {
			return fmt.Errorf("fetch block %d: %w", h, err)
		}

		// Validate block hash
		if err := validateBlockHash(block); err != nil {
			return fmt.Errorf("invalid hash for block %d: %w", h, err)
		}

		// Persist to store
		if st != nil {
			if err := st.PutBlock(block); err != nil {
				return fmt.Errorf("store block %d: %w", h, err)
			}
		}

		if h%100 == 0 || h == peerHeight {
			log.Printf("[sync] synced block %d/%d", h, peerHeight)
		}
	}

	log.Printf("[sync] sync complete — now at height %d", peerHeight)
	return nil
}

// IsSynced returns true when the local chain height matches the peer's height.
func (s *Syncer) IsSynced(peerURL string, prod *producer.BlockProducer) (bool, error) {
	peerLatest, err := s.fetchLatest(peerURL)
	if err != nil {
		return false, fmt.Errorf("fetch peer latest: %w", err)
	}

	localHeight := uint64(0)
	if prod != nil {
		localHeight = prod.GetChainHeight()
	}

	return localHeight >= peerLatest.Height, nil
}

// fetchLatest retrieves the latest block from a peer node.
func (s *Syncer) fetchLatest(peerURL string) (*core.Block, error) {
	url := peerURL + "/api/v1/blocks/latest"
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	var envelope latestResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode latest block: %w", err)
	}
	if !envelope.Success || envelope.Block == nil {
		return nil, fmt.Errorf("peer returned error: %s", envelope.Error)
	}
	return envelope.Block, nil
}

// fetchBlock retrieves a specific block by height from a peer node.
func (s *Syncer) fetchBlock(peerURL string, height uint64) (*core.Block, error) {
	url := fmt.Sprintf("%s/api/v1/blocks/%d", peerURL, height)
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	var envelope blockResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode block %d: %w", height, err)
	}
	if !envelope.Success || envelope.Block == nil {
		return nil, fmt.Errorf("peer returned error for block %d: %s", height, envelope.Error)
	}
	return envelope.Block, nil
}

// validateBlockHash recomputes the block hash and checks it matches the stored value.
func validateBlockHash(block *core.Block) error {
	if block == nil {
		return fmt.Errorf("nil block")
	}
	stored := block.Hash

	// Re-compute the hash using the chain's canonical method
	block.ComputeHash()
	recomputed := block.Hash

	// Restore original (in case the caller reuses the block)
	if stored != recomputed {
		block.Hash = stored // restore original for inspection
		return fmt.Errorf("hash mismatch at height %d: stored=%q computed=%q",
			block.Height, stored, recomputed)
	}
	return nil
}
