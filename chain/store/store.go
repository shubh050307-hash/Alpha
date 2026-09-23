// Package store implements the persistent BadgerDB state store for Alpha Network.
// It provides both low-level key-value operations and higher-level typed
// methods for blocks, agents, balances, and intelligence records.
package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/data"
)

// Key prefixes for logical namespacing within BadgerDB
const (
	prefixBlock      = "block:"
	prefixAgent      = "agent:"
	prefixBalance    = "balance:"
	prefixIntelRec   = "intel:"
	prefixMeta       = "meta:"
	prefixSnapshot   = "snapshot:"
)

// ErrNotFound is returned when a key doesn't exist in the store.
var ErrNotFound = errors.New("not found")

// Store wraps a BadgerDB instance with typed Alpha Network operations.
type Store struct {
	db *badger.DB
}

// Open opens or creates a BadgerDB database at the specified directory.
// The directory is created if it doesn't exist.
// On crash recovery, corrupted memtable/value-log files are automatically
// removed so the DB can open from the last consistent SST checkpoint.
func Open(dir string) (*Store, error) {
	opts := badger.DefaultOptions(dir)
	opts.Logger = nil // suppress BadgerDB's internal logging

	db, err := badger.Open(opts)
	if err != nil {
		// Attempt crash recovery: corrupted memtable (WAL) blocks DB open.
		// Remove the corrupted .mem file and retry — this discards unflushed
		// entries that were in the active memtable at crash time, but recovers
		// all SST-flushed data (typically >90% of the chain).
		if strings.Contains(err.Error(), "Log truncate required") ||
			strings.Contains(err.Error(), "while opening fid") {
			log.Printf("⚠️  BadgerDB corrupted memtable detected, attempting recovery...")
			if recErr := recoverBadgerDB(dir); recErr != nil {
				return nil, fmt.Errorf("open badger db at %s (recovery failed): %w", dir, recErr)
			}
			log.Printf("♻️  Recovery cleanup complete, retrying open...")
			db, err = badger.Open(opts)
			if err != nil {
				return nil, fmt.Errorf("open badger db at %s (after recovery): %w", dir, err)
			}
			log.Printf("✅ BadgerDB opened after recovery — some recent unflushed data may be lost")
		} else {
			return nil, fmt.Errorf("open badger db at %s: %w", dir, err)
		}
	}
	return &Store{db: db}, nil
}

// recoverBadgerDB removes corrupted memtable and lock files so BadgerDB can
// recover from the last consistent SST checkpoint. This discards unflushed
// WAL/memtable entries but preserves all SST data.
func recoverBadgerDB(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir for recovery: %w", err)
	}
	var removed int
	for _, e := range entries {
		name := e.Name()
		// Remove .mem files (active memtable/WAL — may be corrupted)
		// Remove LOCK file (stale lock from crashed process)
		if strings.HasSuffix(name, ".mem") || name == "LOCK" {
			path := filepath.Join(dir, name)
			if rmErr := os.Remove(path); rmErr != nil {
				log.Printf("⚠️  Failed to remove %s: %v", name, rmErr)
			} else {
				log.Printf("🗑  Removed %s for recovery", name)
				removed++
			}
		}
	}
	if removed == 0 {
		return fmt.Errorf("no recoverable files found in %s", dir)
	}
	return nil
}

// Close flushes and closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- Low-level key-value methods ---

// Set stores a raw key-value pair.
func (s *Store) Set(key, value []byte) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, value)
	})
}

// Get retrieves a value by key. Returns ErrNotFound if the key doesn't exist.
func (s *Store) Get(key []byte) ([]byte, error) {
	var val []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrNotFound
			}
			return err
		}
		val, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Delete removes a key from the store. A missing key is not an error.
func (s *Store) Delete(key []byte) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
}

// Scan returns all values whose keys have the given prefix.
// Values are returned in lexicographic key order.
func (s *Store) Scan(prefix []byte) ([][]byte, error) {
	var results [][]byte
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			results = append(results, val)
		}
		return nil
	})
	return results, err
}

// --- Typed higher-level methods ---

// PutBlock persists a block to the store, keyed by height.
func (s *Store) PutBlock(block *core.Block) error {
	if block == nil {
		return errors.New("block cannot be nil")
	}
	val, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("marshal block: %w", err)
	}
	return s.Set(blockKey(block.Height), val)
}

// GetBlock retrieves a block by height. Returns ErrNotFound if not stored.
func (s *Store) GetBlock(height uint64) (*core.Block, error) {
	val, err := s.Get(blockKey(height))
	if err != nil {
		return nil, err
	}
	var block core.Block
	if err := json.Unmarshal(val, &block); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}
	return &block, nil
}

// PutAgent persists an AgentIdentity to the store, keyed by AgentID.
func (s *Store) PutAgent(agent *core.AgentIdentity) error {
	if agent == nil {
		return errors.New("agent cannot be nil")
	}
	val, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("marshal agent: %w", err)
	}
	return s.Set(agentKey(agent.AgentID), val)
}

// GetAgent retrieves an agent by its AgentID. Returns ErrNotFound if not stored.
func (s *Store) GetAgent(id core.AgentID) (*core.AgentIdentity, error) {
	val, err := s.Get(agentKey(id))
	if err != nil {
		return nil, err
	}
	var agent core.AgentIdentity
	if err := json.Unmarshal(val, &agent); err != nil {
		return nil, fmt.Errorf("unmarshal agent: %w", err)
	}
	return &agent, nil
}

// ListAgents returns all stored agents.
func (s *Store) ListAgents() ([]*core.AgentIdentity, error) {
	vals, err := s.Scan([]byte(prefixAgent))
	if err != nil {
		return nil, err
	}
	agents := make([]*core.AgentIdentity, 0, len(vals))
	for _, v := range vals {
		var a core.AgentIdentity
		if err := json.Unmarshal(v, &a); err != nil {
			return nil, fmt.Errorf("unmarshal agent: %w", err)
		}
		agents = append(agents, &a)
	}
	return agents, nil
}

// PutBalance stores the $ALPHA balance for a given address.
func (s *Store) PutBalance(addr core.Address, amount core.Amount) error {
	val, err := json.Marshal(amount)
	if err != nil {
		return fmt.Errorf("marshal balance: %w", err)
	}
	return s.Set(balanceKey(addr), val)
}

// GetBalance retrieves the balance for an address. Returns 0 if not found.
func (s *Store) GetBalance(addr core.Address) (core.Amount, error) {
	val, err := s.Get(balanceKey(addr))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	var amount core.Amount
	if err := json.Unmarshal(val, &amount); err != nil {
		return 0, fmt.Errorf("unmarshal balance: %w", err)
	}
	return amount, nil
}

// PutIntelligenceRecord persists an intelligence record to the store.
// Key format: intel:<agentID>:<recordID>
func (s *Store) PutIntelligenceRecord(r *data.IntelligenceRecord) error {
	if r == nil {
		return errors.New("intelligence record cannot be nil")
	}
	val, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal intelligence record: %w", err)
	}
	return s.Set(intelKey(r.AgentID, r.RecordID), val)
}

// ScanBalanceEntries returns all persisted balance entries as a map of address → amount.
// Used for ledger recovery at startup.
func (s *Store) ScanBalanceEntries() (map[core.Address]core.Amount, error) {
	result := make(map[core.Address]core.Amount)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixBalance)
		it := txn.NewIterator(opts)
		defer it.Close()

		prefixLen := len(prefixBalance)
		for it.Seek([]byte(prefixBalance)); it.ValidForPrefix([]byte(prefixBalance)); it.Next() {
			item := it.Item()
			key := string(item.Key())
			addr := core.Address(key[prefixLen:])

			val, err := item.ValueCopy(nil)
			if err != nil {
				continue
			}
			var amt core.Amount
			if err := json.Unmarshal(val, &amt); err != nil {
				continue
			}
			result[addr] = amt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetIntelligenceRecords returns all intelligence records for a given agent.
func (s *Store) GetIntelligenceRecords(agentID core.AgentID) ([]*data.IntelligenceRecord, error) {
	prefix := []byte(fmt.Sprintf("%s%s:", prefixIntelRec, agentID))
	vals, err := s.Scan(prefix)
	if err != nil {
		return nil, err
	}
	records := make([]*data.IntelligenceRecord, 0, len(vals))
	for _, v := range vals {
		var rec data.IntelligenceRecord
		if err := json.Unmarshal(v, &rec); err != nil {
			return nil, fmt.Errorf("unmarshal intelligence record: %w", err)
		}
		records = append(records, &rec)
	}
	return records, nil
}

// PutSnapshot writes a ledger snapshot at the given block height.
// The snapshot contains all account balances serialized as JSON.
func (s *Store) PutSnapshot(blockHeight uint64, balances map[core.Address]core.Amount, totalBurned, totalSupply core.Amount) error {
	snap := struct {
		BlockHeight uint64                  `json:"block_height"`
		Balances    map[core.Address]core.Amount `json:"balances"`
		TotalBurned core.Amount              `json:"total_burned"`
		TotalSupply core.Amount              `json:"total_supply"`
	}{
		BlockHeight: blockHeight,
		Balances:    balances,
		TotalBurned: totalBurned,
		TotalSupply: totalSupply,
	}
	val, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	key := fmt.Sprintf("%s%020d", prefixSnapshot, blockHeight)
	return s.Set([]byte(key), val)
}

// GetLatestSnapshot retrieves the most recent ledger snapshot.
// Returns the block height, balances, and metadata. Returns ErrNotFound if no snapshot exists.
func (s *Store) GetLatestSnapshot() (uint64, map[core.Address]core.Amount, core.Amount, core.Amount, error) {
	var latestHeight uint64
	var latestVal []byte

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixSnapshot)
		opts.Reverse = true // newest first
		it := txn.NewIterator(opts)
		defer it.Close()

		if it.Seek([]byte(prefixSnapshot + "99999999999999999999")); it.ValidForPrefix([]byte(prefixSnapshot)) {
			item := it.Item()
			key := string(item.Key())
			// Parse block height from key: "snapshot:00000000000000000123"
			if _, scanErr := fmt.Sscanf(key, prefixSnapshot+"%020d", &latestHeight); scanErr != nil {
				return scanErr
			}
			var copyErr error
			latestVal, copyErr = item.ValueCopy(nil)
			return copyErr
		}
		return ErrNotFound
	})
	if err != nil {
		return 0, nil, 0, 0, err
	}

	var snap struct {
		BlockHeight uint64                  `json:"block_height"`
		Balances    map[core.Address]core.Amount `json:"balances"`
		TotalBurned core.Amount              `json:"total_burned"`
		TotalSupply core.Amount              `json:"total_supply"`
	}
	if err := json.Unmarshal(latestVal, &snap); err != nil {
		return 0, nil, 0, 0, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return snap.BlockHeight, snap.Balances, snap.TotalBurned, snap.TotalSupply, nil
}

// HasGenesisBlock returns true if the genesis block (height 0) is stored.
// Used during startup to determine if chain initialization is needed.
func (s *Store) HasGenesisBlock() bool {
	_, err := s.GetBlock(0)
	return err == nil
}

// HasAnyBlocks returns true if at least one block exists in the store.
// Used as a safety check: if genesis is missing but blocks exist, we're
// in a recovery scenario and should recreate genesis rather than wipe the chain.
func (s *Store) HasAnyBlocks() bool {
	var found bool
	_ = s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixBlock)
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		it.Rewind()
		if it.Valid() {
			found = true
		}
		return nil
	})
	return found
}

// RecreateGenesisBlock creates a genesis block from config without wiping
// existing chain state. Used when the genesis block was lost (e.g., compaction)
// but other blocks and snapshots are intact.
func (s *Store) RecreateGenesisBlock(genConfig interface{}) error {
	// Parse genesis time from config
	cfgJSON, err := json.Marshal(genConfig)
	if err != nil {
		return fmt.Errorf("marshal genesis config: %w", err)
	}
	var cfg struct {
		GenesisTime string `json:"genesis_time"`
	}
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return fmt.Errorf("unmarshal genesis config: %w", err)
	}

	genTime, err := time.Parse(time.RFC3339, cfg.GenesisTime)
	if err != nil {
		// Try other formats
		genTime, err = time.Parse("2006-01-02T15:04:05Z", cfg.GenesisTime)
		if err != nil {
			genTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		}
	}

	genBlock := core.Block{
		Height:       0,
		Timestamp:    genTime.UnixMilli(),
		PrevHash:     "0000000000000000000000000000000000000000000000000000000000000000",
		Transactions: []*core.Transaction{},
		ValidatorID:  "genesis",
	}
	genBlock.ComputeHash()

	return s.PutBlock(&genBlock)
}

// PutMeta stores a metadata value (used for chain metadata like genesis hash, etc.)
func (s *Store) PutMeta(key string, value []byte) error {
	return s.Set([]byte(prefixMeta+key), value)
}

// GetMeta retrieves a metadata value. Returns ErrNotFound if absent.
func (s *Store) GetMeta(key string) ([]byte, error) {
	return s.Get([]byte(prefixMeta + key))
}

// --- Key helpers ---

func blockKey(height uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, height)
	return append([]byte(prefixBlock), buf...)
}

func agentKey(id core.AgentID) []byte {
	return []byte(prefixAgent + string(id))
}

func balanceKey(addr core.Address) []byte {
	return []byte(prefixBalance + string(addr))
}

func intelKey(agentID core.AgentID, recordID string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", prefixIntelRec, agentID, recordID))
}
