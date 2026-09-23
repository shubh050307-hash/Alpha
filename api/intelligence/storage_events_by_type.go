// api/intelligence/storage_events_by_type.go
//
// GetEventsByType — generic event scanner filtered by TxType.
// ONLY ADD THIS FILE IF `GetEventsByType` does not already exist in storage.go.
// Check first: grep -n "GetEventsByType" api/intelligence/storage.go
//
// If it already exists with a different signature, adapt storage_rewards.go
// to call the existing method instead of duplicating this one.

package intelligence

import (
	"encoding/json"

	"github.com/dgraph-io/badger/v4"
	"github.com/alpha-network/alpha/chain/types"
)

// GetEventsByType scans the events keyspace and returns all IntelligenceEvent
// records matching the given TxType. Mirrors the prefix-scan pattern already
// used by GetChallengesByStatus in storage_challenge.go.
func (s *Storage) GetEventsByType(txType string) ([]*types.IntelligenceEvent, error) {
	var results []*types.IntelligenceEvent
	prefix := []byte("intel:event:")

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			var ev types.IntelligenceEvent
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &ev)
			})
			if err != nil {
				continue // skip corrupt records
			}
			if ev.Type == txType {
				results = append(results, &ev)
			}
		}
		return nil
	})

	return results, err
}
