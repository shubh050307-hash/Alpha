// chain/types/intelligence_challenge.go
//
// Grand Challenge types — supports auto-close monitor, IQ-weighted scoring, and reward distribution.
// ChallengeRecord moved here from api/intelligence/storage.go to keep types centralized.

package types

// ─── Challenge Status ─────────────────────────────────────────────────────────

// ChallengeStatus tracks the lifecycle of a challenge.
type ChallengeStatus string

const (
	ChallengeStatusPending ChallengeStatus = "pending" // seeded, not yet open
	ChallengeStatusOpen    ChallengeStatus = "open"    // accepting solutions
	ChallengeStatusClosed  ChallengeStatus = "closed"  // winners decided
)

// ─── ChallengeRecord — persisted in BadgerDB via intelligence.Storage ─────────

// ChallengeRecord holds the full state of a Grand Challenge.
type ChallengeRecord struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Category       string          `json:"category"`
	Difficulty     string          `json:"difficulty"`
	PrizePool      float64         `json:"prize_pool"`
	Status         ChallengeStatus `json:"status"`
	CreatedAt      int64           `json:"created_at"`
	DeadlineUnix   int64           `json:"deadline_unix"`
	MinAgents      int             `json:"min_agents"`
	TotalSolutions int             `json:"total_solutions"`
	TotalVotes     int             `json:"total_votes"`
	WinnerIDs      []string        `json:"winner_ids,omitempty"`
	ClosedAt       int64           `json:"closed_at"`
}

// ─── Vote Types ───────────────────────────────────────────────────────────────

// VoteDecision is the direction of a vote.
type VoteDecision string

const (
	VoteApprove VoteDecision = "approve"
	VoteReject  VoteDecision = "reject"
)

// VoteRecord represents one agent's vote on a solution.
type VoteRecord struct {
	VoterAddress string       `json:"voter_address"`
	VoterIQScore float64      `json:"voter_iq_score"` // from Intelligence Leaderboard at vote time
	Confidence   float64      `json:"confidence"`     // 0.0–1.0
	Decision     VoteDecision `json:"decision"`
	Timestamp    int64        `json:"timestamp"`
}

// ─── Solution Types ───────────────────────────────────────────────────────────

// SolutionRecord represents one submitted solution to a challenge.
type SolutionRecord struct {
	ID           string        `json:"id"`
	ChallengeID  string        `json:"challenge_id"`
	AgentAddress string        `json:"agent_address"`
	Content      string        `json:"content"`
	SubmittedAt  int64         `json:"submitted_at"`
	Votes        []*VoteRecord `json:"votes"` // populated when fetching for scoring
}
