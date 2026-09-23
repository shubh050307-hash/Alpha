// api/intelligence/storage_challenge.go
//
// Storage extension methods for Grand Challenge lifecycle.
// These extend the existing Storage struct in storage.go — do NOT modify that file.
// All methods use the existing StoreEvent() and StoreChallenge() patterns.

package intelligence

import (
	"fmt"
	"log"
	"time"

	"github.com/alpha-network/alpha/chain/types"
)

// ─── Read ─────────────────────────────────────────────────────────────────────

// GetOpenChallenges returns all challenges with Status == ChallengeStatusOpen.
func (s *Storage) GetOpenChallenges() ([]*types.ChallengeRecord, error) {
	return s.GetChallengesByStatus(types.ChallengeStatusOpen)
}

// GetNextPendingChallenge returns the earliest-created challenge with Status == Pending,
// ready to be opened after the current one closes.
func (s *Storage) GetNextPendingChallenge() (*types.ChallengeRecord, error) {
	pending, err := s.GetChallengesByStatus(types.ChallengeStatusPending)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	// Return the oldest pending (lowest CreatedAt), so challenges open in order
	oldest := pending[0]
	for _, ch := range pending[1:] {
		if ch.CreatedAt < oldest.CreatedAt {
			oldest = ch
		}
	}
	return oldest, nil
}

// GetSolutionsForChallenge fetches all SolutionRecords submitted to a challenge,
// including their embedded vote slices (populated by storage layer).
func (s *Storage) GetSolutionsForChallenge(challengeID string) ([]*types.SolutionRecord, error) {
	prefix := fmt.Sprintf("solution:%s:", challengeID)
	return s.getSolutionsByPrefix(prefix)
}

// ─── Write ────────────────────────────────────────────────────────────────────

// StoreRewardTx writes a TxTypeReward event to the chain store.
// This is permanent, on-chain proof of winning. When mainnet token launches,
// the SDK reads these records to determine real SPL transfer amounts.
func (s *Storage) StoreRewardTx(reward *types.RewardData) error {
	event := &types.IntelligenceEvent{
		TxHash:       generateTxHash(),
		Type:         types.TxTypeReward,
		Timestamp:    time.Now().UTC(),
		AgentAddress: reward.RecipientAddress,
		Reward:       reward,
	}
	return s.StoreEvent(event)
}

// StoreChallengeTx writes a TxTypeChallengeClose event to the chain store.
// This is the authoritative close record — immutable, queryable by the SDK.
func (s *Storage) StoreChallengeTx(closeData *types.ChallengeCloseData) error {
	event := &types.IntelligenceEvent{
		TxHash:       generateTxHash(),
		Type:         types.TxTypeChallengeClose,
		Timestamp:    time.Now().UTC(),
		AgentAddress: closeData.ChallengeID, // indexed by challenge ID for easy lookup
		ChallengeClose: closeData,
	}
	return s.StoreEvent(event)
}

// MarkChallengeClosed updates the ChallengeRecord status to Closed and sets ClosedAt.
// Uses the existing StoreChallenge() to overwrite the record in BadgerDB.
func (s *Storage) MarkChallengeClosed(challengeID string, closedAt int64) error {
	ch, err := s.GetChallengeByID(challengeID)
	if err != nil {
		return fmt.Errorf("GetChallengeByID: %w", err)
	}

	ch.Status = types.ChallengeStatusClosed
	ch.ClosedAt = closedAt

	return s.StoreChallenge(ch)
}

// SeedInitialChallenges patches existing challenge_001 (adds deadline if missing)
// and seeds challenge_002 as pending so the arena never goes empty.
// Safe to call on every startup — checks existence before creating.
func (s *Storage) SeedInitialChallenges() {
	now := time.Now().Unix()

	// Patch challenge_001: add DeadlineUnix if not set
	ch001, err := s.GetChallengeByID("challenge_001")
	if err == nil {
		if ch001.DeadlineUnix == 0 {
			ch001.DeadlineUnix = now + 72*3600 // fresh 72h window
			_ = s.StoreChallenge(ch001)
			log.Printf("[Seed] Patched challenge_001 deadline to %d", ch001.DeadlineUnix)
		}
		if ch001.PrizePool == 0 {
			ch001.PrizePool = 10000.0
			_ = s.StoreChallenge(ch001)
			log.Printf("[Seed] Patched challenge_001 prize pool to 10000")
		}
	} else {
		log.Printf("[Seed] challenge_001 not found — creating new")
		ch001 = &types.ChallengeRecord{
			ID:           "challenge_001",
			Title:        "Grand Challenge 001",
			Description:  "The inaugural Grand Challenge",
			Category:     "general",
			Difficulty:   "grand",
			PrizePool:    10000.0,
			Status:       types.ChallengeStatusOpen,
			CreatedAt:    now,
			DeadlineUnix: now + 72*3600,
		}
		_ = s.StoreChallenge(ch001)
		log.Println("[Seed] Created challenge_001")
	}

	// Seed challenge_002 as pending (auto-open after 001 closes)
	_, err = s.GetChallengeByID("challenge_002")
	if err != nil {
		ch002 := &types.ChallengeRecord{
			ID:        "challenge_002",
			Title:     "Grand Challenge 002: [Next Frontier]",
			Status:    types.ChallengeStatusPending,
			PrizePool: 10000.0,
			CreatedAt: now,
		}
		_ = s.StoreChallenge(ch002)
		log.Println("[Seed] Created challenge_002 (pending)")
	}
}
