// api/intelligence/challenge_monitor.go
//
// Grand Challenge Auto-Close Monitor
// Runs a goroutine every 5 minutes, checks all OPEN challenges,
// closes when: time >= deadline OR (solutions >= 20 AND votes >= 50)
// On close: calculates weighted winners, writes TxTypeReward + TxTypeChallengeClose

package intelligence

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/alpha-network/alpha/chain/types"
)

const (
	monitorInterval      = 5 * time.Minute
	minSolutionsToClose  = 20
	minVotesToClose      = 50

	prizeShareFirst  = 0.60
	prizeShareSecond = 0.30
	prizeShareThird  = 0.10

	reasonReward = "Grand Challenge Winner"
)

// StartChallengeMonitor launches the background goroutine.
// Call once from your intelligence API server startup.
func StartChallengeMonitor(storage *Storage) {
	go func() {
		log.Println("[ChallengeMonitor] Started — checking every 5 minutes")
		ticker := time.NewTicker(monitorInterval)
		defer ticker.Stop()

		// Run once immediately on startup, then on tick
		runMonitorCycle(storage)
		for range ticker.C {
			runMonitorCycle(storage)
		}
	}()
}

// runMonitorCycle fetches all open challenges and processes each one.
func runMonitorCycle(storage *Storage) {
	challenges, err := storage.GetOpenChallenges()
	if err != nil {
		log.Printf("[ChallengeMonitor] Error fetching open challenges: %v", err)
		return
	}

	for _, ch := range challenges {
		if shouldClose(ch) {
			log.Printf("[ChallengeMonitor] Closing challenge %s (ID: %s)", ch.Title, ch.ID)
			if err := closeChallenge(storage, ch); err != nil {
				log.Printf("[ChallengeMonitor] Error closing challenge %s: %v", ch.ID, err)
			}
		}
	}
}

// shouldClose returns true if the combo trigger fires.
func shouldClose(ch *types.ChallengeRecord) bool {
	now := time.Now().Unix()

	// Trigger 1: deadline passed
	if now >= ch.DeadlineUnix {
		log.Printf("[ChallengeMonitor] Challenge %s: deadline reached", ch.ID)
		return true
	}

	// Trigger 2: enough data to decide
	if ch.TotalSolutions >= minSolutionsToClose && ch.TotalVotes >= minVotesToClose {
		log.Printf("[ChallengeMonitor] Challenge %s: solution/vote threshold met (%d solutions, %d votes)",
			ch.ID, ch.TotalSolutions, ch.TotalVotes)
		return true
	}

	return false
}

// closeChallenge orchestrates the full close sequence:
//  1. Score all solutions with IQ-weighted votes
//  2. Pick top 3 winners
//  3. Write TxTypeReward for each winner
//  4. Write TxTypeChallengeClose
//  5. Mark challenge as closed in storage
//  6. Optionally open next challenge
func closeChallenge(storage *Storage, ch *types.ChallengeRecord) error {
	// 1. Fetch all solutions + their votes
	solutions, err := storage.GetSolutionsForChallenge(ch.ID)
	if err != nil {
		return fmt.Errorf("GetSolutionsForChallenge: %w", err)
	}

	if len(solutions) == 0 {
		log.Printf("[ChallengeMonitor] Challenge %s has no solutions — closing with no winners", ch.ID)
		return writeChallengeCloseTx(storage, ch, nil)
	}

	// 2. Score solutions with IQ-weighted vote model
	scored := scoreAllSolutions(solutions)

	// 3. Pick top 3
	winners := pickTopN(scored, 3)

	// 4. Calculate prize amounts
	prizes := splitPrizePool(ch.PrizePool, len(winners))

	// 5. Write TxTypeReward for each winner
	winnerIDs := make([]string, 0, len(winners))
	for i, w := range winners {
		reward := &types.RewardData{
			RecipientAddress: w.AgentAddress,
			Amount:           prizes[i],
			Reason:           reasonReward,
			Rank:             i + 1,
			ChallengeID:      ch.ID,
		}
		if err := storage.StoreRewardTx(reward); err != nil {
			return fmt.Errorf("StoreRewardTx rank %d: %w", i+1, err)
		}
		log.Printf("[ChallengeMonitor] Reward written — Rank %d: %s → %.0f $ALPHA (score: %.4f)",
			i+1, w.AgentAddress, prizes[i], w.WeightedScore)
		winnerIDs = append(winnerIDs, w.SolutionID)
	}

	// 6. Write TxTypeChallengeClose
	return writeChallengeCloseTx(storage, ch, winnerIDs)
}

// writeChallengeCloseTx writes the permanent close record and marks challenge closed.
func writeChallengeCloseTx(storage *Storage, ch *types.ChallengeRecord, winnerIDs []string) error {
	closeData := &types.ChallengeCloseData{
		ChallengeID: ch.ID,
		ClosedAt:    time.Now().Unix(),
		WinnerIDs:   winnerIDs,
		Reason:      closedReason(ch),
	}
	if err := storage.StoreChallengeTx(closeData); err != nil {
		return fmt.Errorf("StoreChallengeTx: %w", err)
	}

	// Mark the challenge record itself as closed
	if err := storage.MarkChallengeClosed(ch.ID, closeData.ClosedAt); err != nil {
		return fmt.Errorf("MarkChallengeClosed: %w", err)
	}

	log.Printf("[ChallengeMonitor] Challenge %s closed on-chain. Winners: %v", ch.ID, winnerIDs)

	// Auto-open next challenge from queue
	go tryOpenNextChallenge(storage)

	return nil
}

// closedReason returns a human-readable string for why the challenge was closed.
func closedReason(ch *types.ChallengeRecord) string {
	if time.Now().Unix() >= ch.DeadlineUnix {
		return "deadline_reached"
	}
	return "threshold_met"
}

// ─── Scoring ─────────────────────────────────────────────────────────────────

// ScoredSolution holds a solution with its computed weighted score.
type ScoredSolution struct {
	SolutionID    string
	AgentAddress  string
	WeightedScore float64
}

// scoreAllSolutions computes the IQ-weighted vote score for every solution.
//
// score = Σ(confidence × voter_iq) for APPROVE votes
//       - Σ(confidence × voter_iq) for REJECT votes
//
// This makes the system sybil-resistant: 1 Elite agent > 10 Seed agents.
func scoreAllSolutions(solutions []*types.SolutionRecord) []ScoredSolution {
	scored := make([]ScoredSolution, 0, len(solutions))

	for _, sol := range solutions {
		var score float64
		for _, vote := range sol.Votes {
			weight := vote.Confidence * vote.VoterIQScore
			if vote.Decision == types.VoteApprove {
				score += weight
			} else if vote.Decision == types.VoteReject {
				score -= weight
			}
		}
		scored = append(scored, ScoredSolution{
			SolutionID:    sol.ID,
			AgentAddress:  sol.AgentAddress,
			WeightedScore: score,
		})
	}

	return scored
}

// pickTopN sorts by weighted score descending and returns the top N winners.
func pickTopN(scored []ScoredSolution, n int) []ScoredSolution {
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].WeightedScore > scored[j].WeightedScore
	})
	if len(scored) < n {
		return scored
	}
	return scored[:n]
}

// ─── Prize Pool ───────────────────────────────────────────────────────────────

// splitPrizePool splits the pool across up to 3 winners using fixed ratios.
// Handles gracefully if fewer than 3 winners exist.
func splitPrizePool(total float64, numWinners int) []float64 {
	ratios := []float64{prizeShareFirst, prizeShareSecond, prizeShareThird}
	prizes := make([]float64, numWinners)

	if numWinners == 1 {
		prizes[0] = total
		return prizes
	}

	// Distribute by ratio; any leftover from rounding goes to 1st place
	var distributed float64
	for i := 0; i < numWinners && i < len(ratios); i++ {
		prizes[i] = total * ratios[i]
		distributed += prizes[i]
	}

	// Floating point safety: add any dust to winner
	if distributed != total && numWinners > 0 {
		prizes[0] += total - distributed
	}

	return prizes
}

// ─── Auto-Reopen ─────────────────────────────────────────────────────────────

// tryOpenNextChallenge pulls the next pending challenge from the queue and opens it.
// Runs in its own goroutine so it never blocks the close sequence.
func tryOpenNextChallenge(storage *Storage) {
	next, err := storage.GetNextPendingChallenge()
	if err != nil || next == nil {
		log.Println("[ChallengeMonitor] No pending challenges in queue — arena will idle")
		return
	}

	next.Status = types.ChallengeStatusOpen
	next.CreatedAt = time.Now().Unix()
	next.DeadlineUnix = time.Now().Add(72 * time.Hour).Unix()

	if err := storage.StoreChallenge(next); err != nil {
		log.Printf("[ChallengeMonitor] Failed to open next challenge: %v", err)
		return
	}
	log.Printf("[ChallengeMonitor] Auto-opened next challenge: %s", next.Title)
}

// ForceCloseChallenge immediately closes a challenge (admin/testing only).
// Exported for the admin endpoint — gated behind admin key before mainnet.
func ForceCloseChallenge(storage *Storage, challengeID string) error {
	ch, err := storage.GetChallengeByID(challengeID)
	if err != nil {
		return fmt.Errorf("challenge not found: %s", challengeID)
	}
	return closeChallenge(storage, ch)
}
