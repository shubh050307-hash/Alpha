// api/intelligence/storage_rewards.go
//
// GetRewardsByAddress — scans TxTypeReward events and returns all rewards
// earned by a specific agent address. Powers the /rewards/{address} endpoint
// and, downstream, `alpha-agent info` / `alpha-agent withdraw` in the SDK.

package intelligence

import (
	"fmt"

	"github.com/alpha-network/alpha/chain/types"
)

// RewardRecord is the fully-hydrated reward, including the event timestamp
// (RewardData itself doesn't carry a timestamp — that lives on the wrapping
// IntelligenceEvent — so we merge them here for convenience).
type RewardRecord struct {
	ChallengeID      string
	RecipientAddress string
	Amount           float64
	Reason           string
	Rank             int
	Timestamp        int64
}

// GetRewardsByAddress scans all stored TxTypeReward events and returns
// only those matching the given recipient address, newest first.
//
// Implementation matches the existing event-scan pattern used elsewhere
// in storage.go (prefix scan over BadgerDB events keyspace, filtered by type).
func (s *Storage) GetRewardsByAddress(address string) ([]*RewardRecord, error) {
	events, err := s.GetEventsByType(types.TxTypeReward)
	if err != nil {
		return nil, fmt.Errorf("GetEventsByType(TxTypeReward): %w", err)
	}

	var results []*RewardRecord
	for _, event := range events {
		if event.Reward == nil {
			continue
		}
		rd := event.Reward
		if rd.RecipientAddress != address {
			continue
		}
		results = append(results, &RewardRecord{
			ChallengeID:      rd.ChallengeID,
			RecipientAddress: rd.RecipientAddress,
			Amount:           rd.Amount,
			Reason:           rd.Reason,
			Rank:             rd.Rank,
			Timestamp:        event.Timestamp.Unix(),
		})
	}

	// Newest first
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}

	return results, nil
}
