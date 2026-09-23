// Package p2p implements peer-to-peer networking for Alpha Network.
//
// The P2PNode ties together peer discovery (PeerStore), block synchronization
// (Syncer), block gossip, and multi-validator consensus coordination.
//
// Architecture:
//   - Peer discovery: HTTP-based announce/bootstrap (no libp2p dependency)
//   - Block propagation: new blocks are POSTed to all known peers
//   - Block gossip: peers POST blocks they receive to other peers
//   - Sync: nodes catch up via sequential block fetch on startup and periodically
//   - Multi-node PoI: validators submit proofs to their local node; cross-node
//     consensus is coordinated through the P2PNode
package p2p

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	stdsync "sync"
	"time"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/producer"
	"github.com/alpha-network/alpha/chain/store"
	"github.com/alpha-network/alpha/chain/sync"
)

// BlockHandler is called when a valid block is received from a peer.
// The handler should validate and incorporate the block into the local chain.
type BlockHandler func(block *core.Block) error

// P2PNode orchestrates peer-to-peer communication for a single Alpha node.
type P2PNode struct {
	peerStore    *PeerStore
	syncer       *sync.Syncer
	store        *store.Store
	prod         *producer.BlockProducer
	blockHandler BlockHandler

	ownAddr    string   // "host:port" of this node
	ownPort    int
	seedPeers  []string // initial peers to bootstrap from

	// Deduplication cache — prevents gossip loops and re-processing of known blocks
	seenBlocks map[string]time.Time // block hash → when first seen
	seenMu     stdsync.RWMutex

	mu      stdsync.RWMutex
	running bool
	stopCh  chan struct{}
}

// Config holds P2PNode initialization parameters.
type Config struct {
	MyAddress  string   // this node's external address
	MyPort     int      // this node's API port
	SeedPeers  []string // "host:port" entries to bootstrap from (may be empty)
	Store      *store.Store
	Producer   *producer.BlockProducer
	BlockHandler BlockHandler
}

// NewP2PNode creates a P2P node ready to start.
func NewP2PNode(cfg Config) *P2PNode {
	ps := NewPeerStore()
	// Pre-populate seed peers so they're known immediately
	for _, addr := range cfg.SeedPeers {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			log.Printf("[p2p] invalid seed peer %q: %v", addr, err)
			continue
		}
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		ps.Add(&Peer{
			Address:  host,
			Port:     port,
			LastSeen: time.Now().Unix(),
		})
	}

	return &P2PNode{
		peerStore:    ps,
		syncer:       sync.NewSyncer(),
		store:        cfg.Store,
		prod:         cfg.Producer,
		blockHandler: cfg.BlockHandler,
		ownAddr:      cfg.MyAddress,
		ownPort:      cfg.MyPort,
		seedPeers:    cfg.SeedPeers,
		seenBlocks:   make(map[string]time.Time),
		stopCh:       make(chan struct{}),
	}
}

// PeerStore returns the underlying PeerStore (used by the API server).
func (n *P2PNode) PeerStore() *PeerStore { return n.peerStore }

// Syncer returns the underlying Syncer.
func (n *P2PNode) Syncer() *sync.Syncer { return n.syncer }

// Start launches the background P2P loop.
func (n *P2PNode) Start() {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return
	}
	n.running = true
	n.mu.Unlock()

	log.Printf("[p2p] node starting — my address: %s:%d, seed peers: %v", n.ownAddr, n.ownPort, n.seedPeers)

	// Bootstrap from seed peers on startup
	if len(n.seedPeers) > 0 {
		n.peerStore.Bootstrap(n.seedPeers)
	}

	// Sync from a random peer on startup
	n.syncFromBestPeer()

	go n.loop()
}

// Stop gracefully shuts down the P2P node.
func (n *P2PNode) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.running {
		return
	}
	n.running = false
	close(n.stopCh)
	log.Printf("[p2p] node stopped")
}

// BroadcastBlock sends a newly produced block to all known peers.
// This is called by the BlockProducer after producing a block.
func (n *P2PNode) BroadcastBlock(block *core.Block) {
	if block == nil {
		return
	}

	body, err := json.Marshal(map[string]interface{}{
		"block": block,
	})
	if err != nil {
		log.Printf("[p2p] broadcast: marshal block %d: %v", block.Height, err)
		return
	}

	peers := n.peerStore.List()
	for _, p := range peers {
		go func(peer *Peer) {
			url := peer.URL() + "/api/v1/p2p/block"
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				// Silently skip — peer may be offline
				return
			}
			resp.Body.Close()
		}(p)
	}

	if len(peers) > 0 {
		log.Printf("[p2p] broadcast block %d to %d peers", block.Height, len(peers))
	}
}

// HandleIncomingBlock is the HTTP handler for POST /api/v1/p2p/block.
// It receives a block from a peer, validates its hash, stores it, and
// re-gossips it to other peers (except the sender).
// Deduplication: blocks already seen (by hash) are silently dropped to prevent
// gossip amplification loops.
func (n *P2PNode) HandleIncomingBlock(block *core.Block, senderAddr string) error {
	if block == nil {
		return fmt.Errorf("nil block")
	}

	// ── Deduplication check ────────────────────────────────────
	// Gossip loops are a real threat in P2P networks: the same block
	// arrives from multiple peers within milliseconds. We track every
	// block hash we've seen in the last 10 minutes and silently drop
	// duplicates to prevent CPU/bandwidth waste.
	if n.IsBlockSeen(block.Hash) {
		return nil // not an error — normal P2P gossip redundancy
	}
	n.MarkBlockSeen(block.Hash)
	// ── End dedup ───────────────────────────────────────────────

	// Validate the block hash
	stored := block.Hash
	block.ComputeHash()
	if stored != block.Hash {
		block.Hash = stored // restore
		return fmt.Errorf("block %d hash mismatch from peer %s", block.Height, senderAddr)
	}

	// Delegate to the chain-level handler (incorporate into local chain)
	if n.blockHandler != nil {
		if err := n.blockHandler(block); err != nil {
			return fmt.Errorf("incorporate block %d: %w", block.Height, err)
		}
	}

	// Re-gossip to other peers (except sender)
	go n.gossipBlock(block, senderAddr)

	return nil
}

// gossipBlock forwards a received block to other known peers.
func (n *P2PNode) gossipBlock(block *core.Block, excludeAddr string) {
	body, err := json.Marshal(map[string]interface{}{
		"block": block,
	})
	if err != nil {
		return
	}

	peers := n.peerStore.List()
	count := 0
	for _, p := range peers {
		peerAddr := fmt.Sprintf("%s:%d", p.Address, p.Port)
		if peerAddr == excludeAddr {
			continue
		}
		count++
		go func(peer *Peer) {
			url := peer.URL() + "/api/v1/p2p/block"
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				return
			}
			resp.Body.Close()
		}(p)
	}
}

// --- background loop ---

func (n *P2PNode) loop() {
	announceTicker := time.NewTicker(30 * time.Second)
	syncTicker := time.NewTicker(60 * time.Second)
	gcTicker := time.NewTicker(5 * time.Minute) // evict stale seen-block entries
	defer announceTicker.Stop()
	defer syncTicker.Stop()
	defer gcTicker.Stop()

	for {
		select {
		case <-n.stopCh:
			return
		case <-announceTicker.C:
			n.announce()
		case <-syncTicker.C:
			n.syncFromBestPeer()
		case <-gcTicker.C:
			n.gcSeenBlocks()
		}
	}
}

// announce sends this node's address to all known peers.
func (n *P2PNode) announce() {
	myAddr := fmt.Sprintf("%s:%d", n.ownAddr, n.ownPort)
	n.peerStore.Announce(myAddr, n.seedPeers)
}

// syncFromBestPeer picks the peer with the lowest latency and syncs from it.
func (n *P2PNode) syncFromBestPeer() {
	peers := n.peerStore.List()
	if len(peers) == 0 {
		return
	}

	// Pick the peer with the lowest known latency (or first one)
	bestPeer := peers[0]
	for _, p := range peers[1:] {
		if p.LatencyMs > 0 && (bestPeer.LatencyMs == 0 || p.LatencyMs < bestPeer.LatencyMs) {
			bestPeer = p
		}
	}

	if n.store == nil || n.prod == nil {
		return
	}

	if err := n.syncer.SyncFromPeer(bestPeer.URL(), n.store, n.prod); err != nil {
		log.Printf("[p2p] sync from %s failed: %v", bestPeer.URL(), err)
	}
}

// IsBlockSeen returns true if a block with this hash has been seen recently.
// Used to prevent gossip amplification loops.
func (n *P2PNode) IsBlockSeen(hash string) bool {
	n.seenMu.RLock()
	defer n.seenMu.RUnlock()
	_, ok := n.seenBlocks[hash]
	return ok
}

// MarkBlockSeen records a block hash as seen.
func (n *P2PNode) MarkBlockSeen(hash string) {
	n.seenMu.Lock()
	defer n.seenMu.Unlock()
	n.seenBlocks[hash] = time.Now()
}

// gcSeenBlocks evicts entries older than the TTL to bound memory.
// Block hashes are 64 hex chars (32 bytes), so ~100k entries = ~3 MB.
const seenBlockTTL = 10 * time.Minute

func (n *P2PNode) gcSeenBlocks() {
	n.seenMu.Lock()
	defer n.seenMu.Unlock()
	cutoff := time.Now().Add(-seenBlockTTL)
	for hash, ts := range n.seenBlocks {
		if ts.Before(cutoff) {
			delete(n.seenBlocks, hash)
		}
	}
}

// --- helpers ---

// PeerCount returns the number of known peers.
func (n *P2PNode) PeerCount() int {
	return n.peerStore.Count()
}

// IsRunning returns true if the background loop is active.
func (n *P2PNode) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.running
}
