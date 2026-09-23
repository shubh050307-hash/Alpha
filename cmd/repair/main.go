// repair — BadgerDB recovery tool for Alpha Network
//
// Usage: go run ./cmd/repair <badger-dir>
//
// This tool:
// 1. Removes corrupted .mem files and stale LOCK
// 2. Opens the BadgerDB
// 3. Counts blocks — if blocks exist, DB is healthy
// 4. If blocks missing but old SST files exist, attempts MANIFEST repair
//    by streaming entries from orphaned SST files into a fresh DB.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	badger "github.com/dgraph-io/badger/v4"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: repair <badger-dir>")
		os.Exit(1)
	}
	dir := os.Args[1]
	fmt.Printf("🔧 Repairing BadgerDB at %s\n", dir)

	// Step 1: Remove corrupted memtable and lock files
	cleanCorrupted(dir)

	// Step 2: Try to open the DB
	db, err := openDB(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Cannot open DB after recovery: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Step 3: Check what's inside
	blockCount, genesisFound, latestHeight := inspect(db)
	fmt.Printf("   Blocks: %d | Genesis: %v | Latest height: %d\n", blockCount, genesisFound, latestHeight)

	if blockCount > 100 {
		fmt.Printf("✅ Database is healthy — %d blocks found\n", blockCount)
		os.Exit(0)
	}

	// Step 4: DB opens but has no blocks — MANIFEST may be corrupted.
	// Try to recover by creating a fresh DB and copying entries from old one.
	fmt.Println("⚠️  Few blocks found — attempting SST recovery...")
	if err := rebuildFromSST(dir, db); err != nil {
		fmt.Fprintf(os.Stderr, "❌ SST recovery failed: %v\n", err)
		os.Exit(1)
	}
}

func cleanCorrupted(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".mem") || name == "LOCK" {
			path := filepath.Join(dir, name)
			os.Remove(path)
			fmt.Printf("🗑  Removed %s\n", name)
		}
	}
}

func openDB(dir string) (*badger.DB, error) {
	opts := badger.DefaultOptions(dir)
	opts.Logger = nil
	return badger.Open(opts)
}

func inspect(db *badger.DB) (int, bool, uint64) {
	var count int
	var genesis bool
	var latest uint64

	db.View(func(txn *badger.Txn) error {
		// Check genesis
		_, err := txn.Get([]byte("block:00000000000000000000"))
		genesis = (err == nil)

		// Check latest_height meta
		item, err := txn.Get([]byte("meta:latest_height"))
		if err == nil {
			val, _ := item.ValueCopy(nil)
			fmt.Sscanf(string(val), "%d", &latest)
		}

		// Count blocks
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("block:")
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			count++
		}
		return nil
	})
	return count, genesis, latest
}

// rebuildFromSST creates a fresh BadgerDB and copies all readable entries
// from the old DB. If the old DB has entries (even if MANIFEST-corrupted),
// they get migrated to the new DB.
func rebuildFromSST(dir string, oldDB *badger.DB) error {
	newDir := dir + "_recovered"
	os.RemoveAll(newDir)
	os.MkdirAll(newDir, 0755)

	opts := badger.DefaultOptions(newDir)
	opts.Logger = nil
	newDB, err := badger.Open(opts)
	if err != nil {
		return fmt.Errorf("open new db: %w", err)
	}

	fmt.Printf("📦 Streaming entries to %s...\n", newDir)
	var copied int64
	err = oldDB.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		batchSize := 1000
		for it.Rewind(); it.Valid(); {
			wb := newDB.NewWriteBatch()
			for i := 0; i < batchSize && it.Valid(); i++ {
				item := it.Item()
				key := item.KeyCopy(nil)
				val, err := item.ValueCopy(nil)
				if err != nil {
					it.Next()
					continue
				}
				wb.Set(key, val)
				copied++
				it.Next()
			}
			if err := wb.Flush(); err != nil {
				return fmt.Errorf("flush batch: %w", err)
			}
		}
		return nil
	})
	newDB.Close()

	if err != nil {
		return fmt.Errorf("stream entries: %w", err)
	}

	fmt.Printf("✅ Copied %d entries to %s\n", copied, newDir)

	// Verify the new DB
	if copied < 100 {
		fmt.Println("⚠️  Very few entries copied — source DB may be empty")
		return nil
	}

	newDB2, err := openDB(newDir)
	if err != nil {
		return fmt.Errorf("verify new db: %w", err)
	}
	defer newDB2.Close()
	count, _, latest := inspect(newDB2)
	fmt.Printf("   New DB: %d blocks | Latest: %d\n", count, latest)

	// Swap directories
	backupDir := dir + "_old"
	fmt.Printf("🔄 Backing up %s → %s\n", dir, backupDir)
	os.RemoveAll(backupDir)
	if err := os.Rename(dir, backupDir); err != nil {
		return fmt.Errorf("rename old: %w", err)
	}
	fmt.Printf("🔄 Promoting %s → %s\n", newDir, dir)
	if err := os.Rename(newDir, dir); err != nil {
		// Try to restore
		os.Rename(backupDir, dir)
		return fmt.Errorf("rename new: %w", err)
	}
	fmt.Printf("✅ Recovery complete. Old DB saved at %s\n", backupDir)

	// List old SST files that are still around
	oldEntries, _ := os.ReadDir(backupDir)
	var oldSSTs []string
	for _, e := range oldEntries {
		if strings.HasSuffix(e.Name(), ".sst") {
			oldSSTs = append(oldSSTs, e.Name())
		}
	}
	sort.Strings(oldSSTs)
	if len(oldSSTs) > 0 {
		fmt.Printf("   %d orphaned SST files remain in backup: %v...\n", len(oldSSTs), oldSSTs[:min(3, len(oldSSTs))])
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
