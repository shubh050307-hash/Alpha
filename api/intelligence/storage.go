package intelligence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/alpha-network/alpha/chain/types"
)

// ─────────────────────────────────────────────
// Key prefixes — keeps BadgerDB clean and
// scannable like a mini key-value "tables"
// ─────────────────────────────────────────────
const (
	prefixEvent       = "intel:event:"      // intel:event:{tx_hash}
	prefixByType      = "intel:type:"       // intel:type:{type}:{ts}:{tx_hash}
	prefixByAgent     = "intel:agent:"      // intel:agent:{addr}:{ts}:{tx_hash}
	prefixByChallenge = "intel:challenge:"  // intel:challenge:{id}:{ts}:{tx_hash}
	prefixAgentStats  = "intel:stats:"      // intel:stats:{addr}
	prefixChallenge   = "intel:ch:"         // intel:ch:{challenge_id}  → ChallengeRecord
	prefixModelFeed   = "intel:model:"      // intel:model:{ts}:{tx_hash}
)

// ─────────────────────────────────────────────
// Storage — wraps BadgerDB with intelligence ops
// ─────────────────────────────────────────────
type Storage struct {
	db *badger.DB
}

// NewStorage opens (or creates) the intelligence BadgerDB bucket
// dbPath should be e.g. /var/lib/alphanode/intelligence
func NewStorage(dbPath string) (*Storage, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil // silence noisy badger logs
	opts.SyncWrites = true
	opts.NumVersionsToKeep = 1

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("intelligence storage open: %w", err)
	}
	return &Storage{db: db}, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

// ─────────────────────────────────────────────
// StoreEvent — write one intelligence event
// Writes multiple index keys atomically
// ─────────────────────────────────────────────
func (s *Storage) StoreEvent(event *types.IntelligenceEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("event validation: %w", err)
	}

	data, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("event marshal: %w", err)
	}

	ts := event.Timestamp.UTC().Format(time.RFC3339Nano)

	return s.db.Update(func(txn *badger.Txn) error {
		// Primary: by tx_hash
		if err := txn.Set([]byte(prefixEvent+event.TxHash), data); err != nil {
			return err
		}
		// Index: by type + timestamp (for feed queries)
		typeKey := fmt.Sprintf("%s%s:%s:%s", prefixByType, event.Type, ts, event.TxHash)
		if err := txn.Set([]byte(typeKey), []byte(event.TxHash)); err != nil {
			return err
		}
		// Index: by agent + timestamp
		agentKey := fmt.Sprintf("%s%s:%s:%s", prefixByAgent, event.AgentAddress, ts, event.TxHash)
		if err := txn.Set([]byte(agentKey), []byte(event.TxHash)); err != nil {
			return err
		}
		// Index: by challenge (if applicable)
		challengeID := extractChallengeID(event)
		if challengeID != "" {
			ck := fmt.Sprintf("%s%s:%s:%s", prefixByChallenge, challengeID, ts, event.TxHash)
			if err := txn.Set([]byte(ck), []byte(event.TxHash)); err != nil {
				return err
			}
		}
		// Index: model training feed (solutions + feedback + votes)
		if event.Type == types.TxTypeModelFeedback ||
			event.Type == types.TxTypeSolution ||
			event.Type == types.TxTypeVote {
			mk := fmt.Sprintf("%s%s:%s", prefixModelFeed, ts, event.TxHash)
			if err := txn.Set([]byte(mk), []byte(event.TxHash)); err != nil {
				return err
			}
		}
		// Update agent stats counter
		return s.updateAgentStats(txn, event)
	})
}

// ─────────────────────────────────────────────
// AgentStats — running totals per agent
// ─────────────────────────────────────────────
type AgentStats struct {
	Address           string    `json:"address"`
	TotalEvents       int       `json:"total_events"`
	SolutionsCount    int       `json:"solutions_count"`
	VotesCount        int       `json:"votes_count"`
	LabelsCount       int       `json:"labels_count"`
	WorkCount         int       `json:"work_count"`
	TotalReward       float64   `json:"total_reward"`
	AverageConfidence float64   `json:"average_confidence"`
	LastActive        time.Time `json:"last_active"`
	// Derived
	IQScore   float64 `json:"iq_score"`
	TrustTier string  `json:"trust_tier"`
}

func (s *Storage) updateAgentStats(txn *badger.Txn, event *types.IntelligenceEvent) error {
	statsKey := []byte(prefixAgentStats + event.AgentAddress)
	var stats AgentStats

	item, err := txn.Get(statsKey)
	if err == nil {
		if err2 := item.Value(func(val []byte) error {
			return json.Unmarshal(val, &stats)
		}); err2 != nil {
			stats = AgentStats{Address: event.AgentAddress}
		}
	} else {
		stats = AgentStats{Address: event.AgentAddress}
	}

	stats.TotalEvents++
	stats.LastActive = event.Timestamp

	switch event.Type {
	case types.TxTypeSolution:
		stats.SolutionsCount++
		if event.Solution != nil {
			stats.AverageConfidence = (stats.AverageConfidence*float64(stats.SolutionsCount-1) +
				event.Solution.Confidence) / float64(stats.SolutionsCount)
		}
	case types.TxTypeVote:
		stats.VotesCount++
	case types.TxTypeDataLabel:
		stats.LabelsCount++
	case types.TxTypeAgentWork:
		stats.WorkCount++
		if event.AgentWork != nil {
			stats.TotalReward += event.AgentWork.RewardAmount
		}
	case types.TxTypeReward:
		if event.Reward != nil {
			stats.TotalReward += event.Reward.Amount
		}
	}

	stats.IQScore = calculateIQScore(&stats)
	stats.TrustTier = tierFromIQ(stats.IQScore)

	data, err := json.Marshal(&stats)
	if err != nil {
		return err
	}
	return txn.Set(statsKey, data)
}

func (s *Storage) StoreChallenge(ch *types.ChallengeRecord) error {
	data, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixChallenge+ch.ID), data)
	})
}

func (s *Storage) GetChallenge(challengeID string) (*types.ChallengeRecord, error) {
	var ch types.ChallengeRecord
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixChallenge + challengeID))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &ch)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("challenge not found: %s", challengeID)
	}
	return &ch, err
}

func (s *Storage) ListChallenges(status string, limit int) ([]*types.ChallengeRecord, error) {
	var challenges []*types.ChallengeRecord
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 20
		it := txn.NewIterator(opts)
		defer it.Close()
		prefix := []byte(prefixChallenge)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			if err := item.Value(func(val []byte) error {
				var ch types.ChallengeRecord
				if err := json.Unmarshal(val, &ch); err != nil {
					return nil // skip corrupt
				}
				if status == "" || string(ch.Status) == status {
					challenges = append(challenges, &ch)
				}
				return nil
			}); err != nil {
				return err
			}
			if len(challenges) >= limit {
				break
			}
		}
		return nil
	})
	// Sort newest first
	sort.Slice(challenges, func(i, j int) bool {
		return challenges[i].CreatedAt > challenges[j].CreatedAt
	})
	return challenges, err
}

// ─────────────────────────────────────────────
// Read operations
// ─────────────────────────────────────────────

// GetFeed returns the most recent intelligence events (newest first)
func (s *Storage) GetFeed(limit, offset int) ([]*types.IntelligenceEvent, error) {
	hashes, err := s.scanIndexReverse(prefixByType, limit+offset)
	if err != nil {
		return nil, err
	}
	if offset >= len(hashes) {
		return []*types.IntelligenceEvent{}, nil
	}
	hashes = hashes[offset:]
	if len(hashes) > limit {
		hashes = hashes[:limit]
	}
	return s.fetchEventsByHashes(dedupe(hashes))
}

// GetChallengeEvents returns all events for a specific challenge
func (s *Storage) GetChallengeEvents(challengeID string) ([]*types.IntelligenceEvent, error) {
	prefix := prefixByChallenge + challengeID + ":"
	hashes, err := s.scanIndexReverse(prefix, 1000)
	if err != nil {
		return nil, err
	}
	return s.fetchEventsByHashes(dedupe(hashes))
}

// GetAgentEvents returns all events for a specific agent
func (s *Storage) GetAgentEvents(address string, limit int) ([]*types.IntelligenceEvent, error) {
	prefix := prefixByAgent + address + ":"
	hashes, err := s.scanIndexReverse(prefix, limit)
	if err != nil {
		return nil, err
	}
	return s.fetchEventsByHashes(dedupe(hashes))
}

// GetModelFeed returns events formatted for model training
func (s *Storage) GetModelFeed(limit int) ([]*types.IntelligenceEvent, error) {
	hashes, err := s.scanIndexReverse(prefixModelFeed, limit)
	if err != nil {
		return nil, err
	}
	return s.fetchEventsByHashes(dedupe(hashes))
}

// GetLeaderboard returns top agents by IQ score
func (s *Storage) GetLeaderboard(limit int) ([]*AgentStats, error) {
	var stats []*AgentStats
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 50
		it := txn.NewIterator(opts)
		defer it.Close()
		prefix := []byte(prefixAgentStats)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			if err := item.Value(func(val []byte) error {
				var s AgentStats
				if err := json.Unmarshal(val, &s); err != nil {
					return nil
				}
				stats = append(stats, &s)
				return nil
			}); err != nil {
				continue
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Sort by IQ descending
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].IQScore > stats[j].IQScore
	})
	if len(stats) > limit {
		stats = stats[:limit]
	}
	return stats, nil
}

// GetNetworkStats returns aggregate network intelligence metrics
func (s *Storage) GetNetworkStats() (map[string]interface{}, error) {
	var totalEvents, totalSolutions, totalVotes, totalLabels int
	var totalReward float64
	var agentCount int

	err := s.db.View(func(txn *badger.Txn) error {
		// Count events by type
		for _, typeKey := range []string{
			prefixByType + types.TxTypeSolution,
			prefixByType + types.TxTypeVote,
			prefixByType + types.TxTypeDataLabel,
		} {
			opts := badger.DefaultIteratorOptions
			opts.PrefetchValues = false
			it := txn.NewIterator(opts)
			prefix := []byte(typeKey)
			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				totalEvents++
				switch {
				case strings.Contains(typeKey, types.TxTypeSolution):
					totalSolutions++
				case strings.Contains(typeKey, types.TxTypeVote):
					totalVotes++
				case strings.Contains(typeKey, types.TxTypeDataLabel):
					totalLabels++
				}
			}
			it.Close()
		}
		// Count agents and total rewards
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 20
		it := txn.NewIterator(opts)
		defer it.Close()
		prefix := []byte(prefixAgentStats)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			_ = item.Value(func(val []byte) error {
				var s AgentStats
				if err := json.Unmarshal(val, &s); err == nil {
					agentCount++
					totalReward += s.TotalReward
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"total_events":    totalEvents,
		"total_solutions": totalSolutions,
		"total_votes":     totalVotes,
		"total_labels":    totalLabels,
		"total_reward":    totalReward,
		"agent_count":     agentCount,
		"timestamp":       time.Now().UTC(),
	}, nil
}

// ─────────────────────────────────────────────
// Challenge lifecycle methods (used by monitor)
// ─────────────────────────────────────────────

// GetChallengesByStatus returns all challenges matching the given status.
func (s *Storage) GetChallengesByStatus(status types.ChallengeStatus) ([]*types.ChallengeRecord, error) {
	var results []*types.ChallengeRecord
	prefix := []byte(prefixChallenge)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			var ch types.ChallengeRecord
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &ch)
			})
			if err != nil {
				continue
			}
			if ch.Status == status {
				results = append(results, &ch)
			}
		}
		return nil
	})
	return results, err
}

// GetChallengeByID returns a single challenge by its ID string.
func (s *Storage) GetChallengeByID(id string) (*types.ChallengeRecord, error) {
	return s.GetChallenge(id)
}

// getSolutionsByPrefix scans BadgerDB for solution records matching a key prefix.
// Keys are stored as: solution:{challenge_id}:{solution_id}
func (s *Storage) getSolutionsByPrefix(prefixStr string) ([]*types.SolutionRecord, error) {
	var results []*types.SolutionRecord
	prefixBytes := []byte(prefixStr)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefixBytes
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.ValidForPrefix(prefixBytes); it.Next() {
			item := it.Item()
			var sol types.SolutionRecord
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &sol)
			})
			if err != nil {
				continue
			}
			results = append(results, &sol)
		}
		return nil
	})
	return results, err
}

// ─────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────

// scanIndexReverse scans an index prefix and returns tx_hash values newest-first
func (s *Storage) scanIndexReverse(prefix string, limit int) ([]string, error) {
	var hashes []string
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.PrefetchSize = limit
		it := txn.NewIterator(opts)
		defer it.Close()
		// For reverse scan seek to prefix + \xff
		seekKey := []byte(prefix + "\xff")
		for it.Seek(seekKey); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()
			if err := item.Value(func(val []byte) error {
				hashes = append(hashes, string(val))
				return nil
			}); err != nil {
				return err
			}
			if len(hashes) >= limit {
				break
			}
		}
		return nil
	})
	return hashes, err
}

// fetchEventsByHashes loads full event structs from primary index
func (s *Storage) fetchEventsByHashes(hashes []string) ([]*types.IntelligenceEvent, error) {
	var events []*types.IntelligenceEvent
	err := s.db.View(func(txn *badger.Txn) error {
		for _, hash := range hashes {
			item, err := txn.Get([]byte(prefixEvent + hash))
			if err != nil {
				continue // skip missing
			}
			if err := item.Value(func(val []byte) error {
				event, err := types.FromIntelligenceJSON(val)
				if err != nil {
					return nil // skip corrupt
				}
				events = append(events, event)
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return events, err
}

func extractChallengeID(e *types.IntelligenceEvent) string {
	switch e.Type {
	case types.TxTypeSolution:
		if e.Solution != nil {
			return e.Solution.ChallengeID
		}
	case types.TxTypeVote:
		if e.Vote != nil {
			return e.Vote.ChallengeID
		}
	case types.TxTypeChallengeOpen:
		if e.ChallengeOpen != nil {
			return e.ChallengeOpen.ChallengeID
		}
	case types.TxTypeChallengeClose:
		if e.ChallengeClose != nil {
			return e.ChallengeClose.ChallengeID
		}
	}
	return ""
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// calculateIQScore derives an intelligence score from agent stats
func calculateIQScore(s *AgentStats) float64 {
	base := 100.0
	base += float64(s.WorkCount) * 0.5
	base += float64(s.SolutionsCount) * 2.0
	base += float64(s.VotesCount) * 0.3
	base += float64(s.LabelsCount) * 0.8
	base += s.TotalReward * 0.01
	base += s.AverageConfidence * 20.0
	if base > 200.0 {
		base = 200.0
	}
	return base
}

func tierFromIQ(iq float64) string {
	switch {
	case iq >= 170:
		return "Elite"
	case iq >= 140:
		return "Trusted"
	case iq >= 110:
		return "Active"
	default:
		return "Seed"
	}
}
