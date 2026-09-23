package types

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ─────────────────────────────────────────────
// Intelligence Transaction Type Constants
// ─────────────────────────────────────────────
const (
	TxTypeAgentWork      = "agent_work"         // Agent completed a PoI task
	TxTypeVote           = "intelligence_vote"   // Agent/human voted on a solution
	TxTypeSolution       = "solution_submit"     // Solution submitted to a challenge
	TxTypeDataLabel      = "data_label"          // Human labeled an AI output
	TxTypeReward         = "reward_dist"         // $ALPHA reward distributed
	TxTypeModelFeedback  = "model_feedback"      // Feedback on model output quality
	TxTypeChallengeOpen  = "challenge_open"      // New challenge created by Oracle
	TxTypeChallengeClose = "challenge_close"     // Challenge closed, winners selected
)

// ─────────────────────────────────────────────
// IntelligenceEvent — core on-chain record
// Every intelligence action produces one of these
// ─────────────────────────────────────────────
type IntelligenceEvent struct {
	// Standard chain fields
	TxHash    string    `json:"tx_hash"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Block     uint64    `json:"block"`

	// Who did this
	AgentAddress string  `json:"agent_address"`
	AgentIQ      float64 `json:"agent_iq,omitempty"`

	// Type-specific payload — only one populated per event
	AgentWork      *AgentWorkData      `json:"agent_work,omitempty"`
	Vote           *VoteData           `json:"vote,omitempty"`
	Solution       *SolutionData       `json:"solution,omitempty"`
	DataLabel      *DataLabelData      `json:"data_label,omitempty"`
	Reward         *RewardData         `json:"reward,omitempty"`
	ModelFeedback  *ModelFeedbackData  `json:"model_feedback,omitempty"`
	ChallengeOpen  *ChallengeOpenData  `json:"challenge_open,omitempty"`
	ChallengeClose *ChallengeCloseData `json:"challenge_close,omitempty"`
}

// ─────────────────────────────────────────────
// Payload structs
// ─────────────────────────────────────────────

// AgentWorkData — agent completed a PoI task
type AgentWorkData struct {
	TaskID       string  `json:"task_id"`
	TaskType     string  `json:"task_type"` // image_classification, text_inference, embedding
	LatencyMS    int64   `json:"latency_ms"`
	Accuracy     float64 `json:"accuracy"`    // 0.0 – 1.0
	ProofHash    string  `json:"proof_hash"`  // ZK proof hash
	RewardAmount float64 `json:"reward_amount"`
}

// VoteData — vote on a solution
type VoteData struct {
	ChallengeID string  `json:"challenge_id"`
	SolutionID  string  `json:"solution_id"`
	Vote        string  `json:"vote"`       // "approve" | "reject"
	Reasoning   string  `json:"reasoning"`
	Confidence  float64 `json:"confidence"` // 0.0 – 1.0
}

// SolutionData — solution submitted to a challenge
type SolutionData struct {
	ChallengeID    string   `json:"challenge_id"`
	SolutionID     string   `json:"solution_id"`
	SolutionHash   string   `json:"solution_hash"`  // SHA256 of solution text
	SolutionText   string   `json:"solution_text"`
	Confidence     float64  `json:"confidence"`
	Perspectives   []string `json:"perspectives,omitempty"` // which angles agent covered
	EstimatedScore float64  `json:"estimated_score,omitempty"`
}

// DataLabelData — human labeled an AI output
type DataLabelData struct {
	OutputID     string `json:"output_id"`
	Label        string `json:"label"`         // "correct" | "incorrect" | "needs_improvement"
	FeedbackText string `json:"feedback_text"`
	Category     string `json:"category"`      // "quality" | "safety" | "accuracy" | "relevance"
}

// RewardData — $ALPHA reward distributed on-chain
type RewardData struct {
	RecipientAddress string  `json:"recipient_address"`
	Amount           float64 `json:"amount"`
	Reason           string  `json:"reason"`       // "task_completion" | "challenge_win" | "data_label"
	ReferenceID      string  `json:"reference_id"` // task_id, solution_id, etc.
	Rank             int     `json:"rank,omitempty"` // 1st, 2nd, 3rd for challenges
	ChallengeID      string  `json:"challenge_id,omitempty"` // for Grand Challenge rewards
}

// ModelFeedbackData — feedback on Alpha Model playground output
type ModelFeedbackData struct {
	SessionID    string `json:"session_id"`
	Query        string `json:"query"`
	Response     string `json:"response"`
	Rating       int    `json:"rating"`        // 1–5 stars
	FeedbackText string `json:"feedback_text"`
	Useful       bool   `json:"useful"`
	Category     string `json:"category,omitempty"` // what type of query
}

// ChallengeOpenData — new Grand Challenge opened by Oracle
type ChallengeOpenData struct {
	ChallengeID  string  `json:"challenge_id"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Category     string  `json:"category"`
	Difficulty   string  `json:"difficulty"` // "hard" | "critical" | "grand"
	PrizePool    float64 `json:"prize_pool"` // $ALPHA
	DeadlineUnix int64   `json:"deadline_unix"`
	MinAgents    int     `json:"min_agents"`
}

// ChallengeCloseData — challenge closed, results finalised
type ChallengeCloseData struct {
	ChallengeID    string   `json:"challenge_id"`
	WinnerIDs      []string `json:"winner_ids"`      // top 3 solution IDs
	TotalSolutions int      `json:"total_solutions"`
	TotalVotes     int      `json:"total_votes"`
	NetworkIQDelta float64  `json:"network_iq_delta"` // how much IQ increased
	ClosedAt       int64    `json:"closed_at"`        // Unix timestamp of close
	Reason         string   `json:"reason"`           // "deadline_reached" | "threshold_met"
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

// ToJSON serialises an event for BadgerDB storage
func (e *IntelligenceEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// FromIntelligenceJSON parses a stored event back
func FromIntelligenceJSON(data []byte) (*IntelligenceEvent, error) {
	var event IntelligenceEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

// HashText returns a SHA-256 hex digest — used for solution_hash
func HashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// ─────────────────────────────────────────────
// Validation
// ─────────────────────────────────────────────

func (e *IntelligenceEvent) Validate() error {
	if e.TxHash == "" {
		return ErrInvalidTxHash
	}
	if e.Type == "" {
		return ErrInvalidType
	}
	if e.AgentAddress == "" {
		return ErrInvalidAgent
	}

	switch e.Type {
	case TxTypeAgentWork:
		if e.AgentWork == nil || e.AgentWork.TaskID == "" {
			return ErrInvalidTaskID
		}
	case TxTypeVote:
		if e.Vote == nil || e.Vote.ChallengeID == "" || e.Vote.SolutionID == "" {
			return ErrInvalidVote
		}
		if e.Vote.Vote != "approve" && e.Vote.Vote != "reject" {
			return errors.New("vote must be 'approve' or 'reject'")
		}
		if e.Vote.Confidence < 0 || e.Vote.Confidence > 1 {
			return errors.New("confidence must be between 0 and 1")
		}
	case TxTypeSolution:
		if e.Solution == nil || e.Solution.ChallengeID == "" || e.Solution.SolutionID == "" {
			return ErrInvalidSolution
		}
		if len(e.Solution.SolutionText) < 50 {
			return errors.New("solution text too short (min 50 chars)")
		}
	case TxTypeDataLabel:
		if e.DataLabel == nil || e.DataLabel.OutputID == "" {
			return ErrInvalidOutput
		}
	case TxTypeReward:
		if e.Reward == nil || e.Reward.Amount <= 0 {
			return ErrInvalidAmount
		}
		if e.Reward.RecipientAddress == "" {
			return ErrInvalidAgent
		}
	case TxTypeModelFeedback:
		if e.ModelFeedback == nil || e.ModelFeedback.SessionID == "" {
			return ErrInvalidSession
		}
		if e.ModelFeedback.Rating < 1 || e.ModelFeedback.Rating > 5 {
			return errors.New("rating must be between 1 and 5")
		}
	case TxTypeChallengeOpen:
		if e.ChallengeOpen == nil || e.ChallengeOpen.ChallengeID == "" {
			return errors.New("missing challenge open data")
		}
	case TxTypeChallengeClose:
		if e.ChallengeClose == nil || e.ChallengeClose.ChallengeID == "" {
			return errors.New("missing challenge close data")
		}
	default:
		return ErrUnknownType
	}
	return nil
}

// ─────────────────────────────────────────────
// Sentinel errors
// ─────────────────────────────────────────────

var (
	ErrInvalidTxHash   = errors.New("invalid transaction hash")
	ErrInvalidType     = errors.New("invalid event type")
	ErrInvalidAgent    = errors.New("invalid agent address")
	ErrMissingData     = errors.New("missing type-specific data")
	ErrInvalidTaskID   = errors.New("invalid task ID")
	ErrInvalidVote     = errors.New("invalid vote data")
	ErrInvalidSolution = errors.New("invalid solution data")
	ErrInvalidOutput   = errors.New("invalid output ID")
	ErrInvalidAmount   = errors.New("invalid reward amount")
	ErrInvalidSession  = errors.New("invalid session ID")
	ErrUnknownType     = errors.New("unknown event type")
)
