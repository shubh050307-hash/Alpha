package intelligence

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpha-network/alpha/chain/types"
)

// ─────────────────────────────────────────────
// Handler — holds storage + chain references
// ─────────────────────────────────────────────
type Handler struct {
	storage     *Storage
	chainHeight func() uint64       // injected: returns current chain height
	agentIQ     func(string) float64 // injected: returns agent IQ by address
}

// NewHandler creates a Handler with its dependencies
func NewHandler(storage *Storage, chainHeight func() uint64, agentIQ func(string) float64) *Handler {
	return &Handler{
		storage:     storage,
		chainHeight: chainHeight,
		agentIQ:     agentIQ,
	}
}

// ─────────────────────────────────────────────
// RegisterRoutes wires all /api/v1/intelligence
// routes onto an http.ServeMux or any mux that
// accepts Handle(pattern, handler)
// ─────────────────────────────────────────────
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/intelligence/submit", h.cors(h.SubmitSolution))
	mux.HandleFunc("/api/v1/intelligence/vote", h.cors(h.SubmitVote))
	mux.HandleFunc("/api/v1/intelligence/label", h.cors(h.SubmitLabel))
	mux.HandleFunc("/api/v1/intelligence/feedback", h.cors(h.SubmitFeedback))
	mux.HandleFunc("/api/v1/intelligence/feed", h.cors(h.GetFeed))
	mux.HandleFunc("/api/v1/intelligence/challenge", h.cors(h.ChallengeRouter))
	mux.HandleFunc("/api/v1/intelligence/leaderboard", h.cors(h.GetLeaderboard))
	mux.HandleFunc("/api/v1/intelligence/agent/", h.cors(h.GetAgentHistory))
	// NOTE: /api/v1/intelligence/stats is registered by the existing server routes;
	// the new intelligence layer's stats endpoint is available at:
	mux.HandleFunc("/api/v1/intelligence/stats", h.cors(h.GetNetworkStats))
	mux.HandleFunc("/api/v1/intelligence/model/feed", h.cors(h.GetModelFeed))
}

// ─────────────────────────────────────────────
// POST /api/v1/intelligence/submit
// Agent submits a solution to a Grand Challenge
// ─────────────────────────────────────────────
type SubmitSolutionRequest struct {
	AgentAddress string   `json:"agent_address"`
	ChallengeID  string   `json:"challenge_id"`
	SolutionText string   `json:"solution_text"`
	Confidence   float64  `json:"confidence"`
	Perspectives []string `json:"perspectives,omitempty"`
}

func (h *Handler) SubmitSolution(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SubmitSolutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AgentAddress == "" || req.ChallengeID == "" || req.SolutionText == "" {
		jsonError(w, "agent_address, challenge_id, and solution_text are required", http.StatusBadRequest)
		return
	}
	if len(req.SolutionText) < 50 {
		jsonError(w, "solution_text must be at least 50 characters", http.StatusBadRequest)
		return
	}
	if req.Confidence < 0 || req.Confidence > 1 {
		jsonError(w, "confidence must be between 0.0 and 1.0", http.StatusBadRequest)
		return
	}

	// Verify challenge exists and is open
	ch, err := h.storage.GetChallenge(req.ChallengeID)
	if err != nil {
		jsonError(w, "challenge not found: "+req.ChallengeID, http.StatusNotFound)
		return
	}
	if ch.Status != types.ChallengeStatusOpen {
		jsonError(w, "challenge is closed", http.StatusConflict)
		return
	}

	solutionID := generateID("sol")
	txHash := generateTxHash()
	agentIQ := h.agentIQ(req.AgentAddress)

	event := &types.IntelligenceEvent{
		TxHash:       txHash,
		Type:         types.TxTypeSolution,
		Timestamp:    time.Now().UTC(),
		Block:        h.chainHeight(),
		AgentAddress: req.AgentAddress,
		AgentIQ:      agentIQ,
		Solution: &types.SolutionData{
			ChallengeID:  req.ChallengeID,
			SolutionID:   solutionID,
			SolutionHash: types.HashText(req.SolutionText),
			SolutionText: req.SolutionText,
			Confidence:   req.Confidence,
			Perspectives: req.Perspectives,
		},
	}

	if err := h.storage.StoreEvent(event); err != nil {
		jsonError(w, "failed to store event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update challenge solution count
	ch.TotalSolutions++
	_ = h.storage.StoreChallenge(ch)

	jsonOK(w, map[string]interface{}{
		"tx_hash":      txHash,
		"solution_id":  solutionID,
		"challenge_id": req.ChallengeID,
		"block":        event.Block,
		"timestamp":    event.Timestamp,
		"message":      "Solution recorded on-chain",
	})
}

// ─────────────────────────────────────────────
// POST /api/v1/intelligence/vote
// Agent or human votes on a solution
// ─────────────────────────────────────────────
type SubmitVoteRequest struct {
	AgentAddress string  `json:"agent_address"`
	ChallengeID  string  `json:"challenge_id"`
	SolutionID   string  `json:"solution_id"`
	Vote         string  `json:"vote"`       // "approve" | "reject"
	Reasoning    string  `json:"reasoning"`
	Confidence   float64 `json:"confidence"` // 0.0 – 1.0
}

func (h *Handler) SubmitVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SubmitVoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AgentAddress == "" || req.ChallengeID == "" || req.SolutionID == "" {
		jsonError(w, "agent_address, challenge_id, and solution_id are required", http.StatusBadRequest)
		return
	}
	if req.Vote != "approve" && req.Vote != "reject" {
		jsonError(w, "vote must be 'approve' or 'reject'", http.StatusBadRequest)
		return
	}
	if req.Confidence < 0 || req.Confidence > 1 {
		jsonError(w, "confidence must be between 0.0 and 1.0", http.StatusBadRequest)
		return
	}

	txHash := generateTxHash()
	agentIQ := h.agentIQ(req.AgentAddress)

	event := &types.IntelligenceEvent{
		TxHash:       txHash,
		Type:         types.TxTypeVote,
		Timestamp:    time.Now().UTC(),
		Block:        h.chainHeight(),
		AgentAddress: req.AgentAddress,
		AgentIQ:      agentIQ,
		Vote: &types.VoteData{
			ChallengeID: req.ChallengeID,
			SolutionID:  req.SolutionID,
			Vote:        req.Vote,
			Reasoning:   req.Reasoning,
			Confidence:  req.Confidence,
		},
	}

	if err := h.storage.StoreEvent(event); err != nil {
		jsonError(w, "failed to store event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update challenge vote count
	if ch, err := h.storage.GetChallenge(req.ChallengeID); err == nil {
		ch.TotalVotes++
		_ = h.storage.StoreChallenge(ch)
	}

	jsonOK(w, map[string]interface{}{
		"tx_hash":   txHash,
		"block":     event.Block,
		"timestamp": event.Timestamp,
		"message":   "Vote recorded on-chain",
	})
}

// ─────────────────────────────────────────────
// POST /api/v1/intelligence/label
// Human labels an AI output for model training
// ─────────────────────────────────────────────
type SubmitLabelRequest struct {
	AgentAddress string `json:"agent_address"`
	OutputID     string `json:"output_id"`
	Label        string `json:"label"`        // "correct" | "incorrect" | "needs_improvement"
	FeedbackText string `json:"feedback_text"`
	Category     string `json:"category"`     // "quality" | "safety" | "accuracy" | "relevance"
}

func (h *Handler) SubmitLabel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SubmitLabelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AgentAddress == "" || req.OutputID == "" || req.Label == "" {
		jsonError(w, "agent_address, output_id, and label are required", http.StatusBadRequest)
		return
	}
	validLabels := map[string]bool{"correct": true, "incorrect": true, "needs_improvement": true}
	if !validLabels[req.Label] {
		jsonError(w, "label must be: correct, incorrect, or needs_improvement", http.StatusBadRequest)
		return
	}

	txHash := generateTxHash()
	agentIQ := h.agentIQ(req.AgentAddress)

	event := &types.IntelligenceEvent{
		TxHash:       txHash,
		Type:         types.TxTypeDataLabel,
		Timestamp:    time.Now().UTC(),
		Block:        h.chainHeight(),
		AgentAddress: req.AgentAddress,
		AgentIQ:      agentIQ,
		DataLabel: &types.DataLabelData{
			OutputID:     req.OutputID,
			Label:        req.Label,
			FeedbackText: req.FeedbackText,
			Category:     req.Category,
		},
	}

	if err := h.storage.StoreEvent(event); err != nil {
		jsonError(w, "failed to store event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]interface{}{
		"tx_hash":   txHash,
		"block":     event.Block,
		"timestamp": event.Timestamp,
		"message":   "Label recorded on-chain — thank you for improving the model",
	})
}

// ─────────────────────────────────────────────
// POST /api/v1/intelligence/feedback
// Feedback on Alpha Model Playground output
// ─────────────────────────────────────────────
type SubmitFeedbackRequest struct {
	AgentAddress string `json:"agent_address"`
	SessionID    string `json:"session_id"`
	Query        string `json:"query"`
	Response     string `json:"response"`
	Rating       int    `json:"rating"` // 1–5
	FeedbackText string `json:"feedback_text"`
	Useful       bool   `json:"useful"`
	Category     string `json:"category,omitempty"`
}

func (h *Handler) SubmitFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SubmitFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AgentAddress == "" || req.SessionID == "" {
		jsonError(w, "agent_address and session_id are required", http.StatusBadRequest)
		return
	}
	if req.Rating < 1 || req.Rating > 5 {
		jsonError(w, "rating must be between 1 and 5", http.StatusBadRequest)
		return
	}

	txHash := generateTxHash()
	agentIQ := h.agentIQ(req.AgentAddress)

	event := &types.IntelligenceEvent{
		TxHash:       txHash,
		Type:         types.TxTypeModelFeedback,
		Timestamp:    time.Now().UTC(),
		Block:        h.chainHeight(),
		AgentAddress: req.AgentAddress,
		AgentIQ:      agentIQ,
		ModelFeedback: &types.ModelFeedbackData{
			SessionID:    req.SessionID,
			Query:        req.Query,
			Response:     req.Response,
			Rating:       req.Rating,
			FeedbackText: req.FeedbackText,
			Useful:       req.Useful,
			Category:     req.Category,
		},
	}

	if err := h.storage.StoreEvent(event); err != nil {
		jsonError(w, "failed to store event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Small reward for feedback
	rewardTxHash := generateTxHash()
	rewardAmt := 0.5 * float64(req.Rating)
	rewardEvent := &types.IntelligenceEvent{
		TxHash:       rewardTxHash,
		Type:         types.TxTypeReward,
		Timestamp:    time.Now().UTC(),
		Block:        h.chainHeight(),
		AgentAddress: "oracle",
		Reward: &types.RewardData{
			RecipientAddress: req.AgentAddress,
			Amount:           rewardAmt,
			Reason:           "model_feedback",
			ReferenceID:      txHash,
		},
	}
	_ = h.storage.StoreEvent(rewardEvent)

	jsonOK(w, map[string]interface{}{
		"tx_hash":       txHash,
		"block":         event.Block,
		"timestamp":     event.Timestamp,
		"reward_earned": rewardAmt,
		"message":       fmt.Sprintf("Feedback recorded. You earned %.1f $ALPHA", rewardAmt),
	})
}

// ─────────────────────────────────────────────
// GET /api/v1/intelligence/feed?limit=20&offset=0
// Returns recent intelligence events
// ─────────────────────────────────────────────
func (h *Handler) GetFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := queryInt(r, "limit", 20, 100)
	offset := queryInt(r, "offset", 0, 10000)

	events, err := h.storage.GetFeed(limit, offset)
	if err != nil {
		jsonError(w, "failed to fetch feed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]interface{}{
		"events": events,
		"count":  len(events),
		"limit":  limit,
		"offset": offset,
	})
}

// ─────────────────────────────────────────────
// /api/v1/intelligence/challenge
// Routes:
//   GET  /api/v1/intelligence/challenge?status=open   → list challenges
//   GET  /api/v1/intelligence/challenge?id=xxx        → get one challenge
//   POST /api/v1/intelligence/challenge               → create challenge (oracle only)
// ─────────────────────────────────────────────
func (h *Handler) ChallengeRouter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if id := r.URL.Query().Get("id"); id != "" {
			h.GetChallenge(w, r, id)
		} else {
			h.ListChallenges(w, r)
		}
	case http.MethodPost:
		h.CreateChallenge(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) GetChallenge(w http.ResponseWriter, r *http.Request, id string) {
	ch, err := h.storage.GetChallenge(id)
	if err != nil {
		jsonError(w, "challenge not found", http.StatusNotFound)
		return
	}
	// Also fetch events for this challenge
	events, _ := h.storage.GetChallengeEvents(id)
	jsonOK(w, map[string]interface{}{
		"challenge": ch,
		"events":    events,
	})
}

func (h *Handler) ListChallenges(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status") // "open", "closed", or "" for all
	limit := queryInt(r, "limit", 20, 100)
	challenges, err := h.storage.ListChallenges(status, limit)
	if err != nil {
		jsonError(w, "failed to list challenges: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"challenges": challenges,
		"count":      len(challenges),
	})
}

type CreateChallengeRequest struct {
	OracleKey     string  `json:"oracle_key"` // simple shared secret for now
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Category      string  `json:"category"`
	Difficulty    string  `json:"difficulty"`
	PrizePool     float64 `json:"prize_pool"`
	DurationHours int     `json:"duration_hours"`
	MinAgents     int     `json:"min_agents"`
}

func (h *Handler) CreateChallenge(w http.ResponseWriter, r *http.Request) {
	var req CreateChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.Description == "" {
		jsonError(w, "title and description are required", http.StatusBadRequest)
		return
	}
	if req.PrizePool <= 0 {
		jsonError(w, "prize_pool must be > 0", http.StatusBadRequest)
		return
	}
	if req.DurationHours <= 0 {
		req.DurationHours = 72 // default 72 hours
	}
	if req.MinAgents <= 0 {
		req.MinAgents = 10
	}

	challengeID := generateID("ch")
	now := time.Now().UTC()
	deadline := now.Add(time.Duration(req.DurationHours) * time.Hour)
	txHash := generateTxHash()

	ch := &types.ChallengeRecord{
		ID:           challengeID,
		Title:        req.Title,
		Description:  req.Description,
		Category:     req.Category,
		Difficulty:   req.Difficulty,
		PrizePool:    req.PrizePool,
		Status:       types.ChallengeStatusOpen,
		CreatedAt:    now.Unix(),
		DeadlineUnix: deadline.Unix(),
		MinAgents:    req.MinAgents,
	}

	if err := h.storage.StoreChallenge(ch); err != nil {
		jsonError(w, "failed to store challenge: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Record on-chain event
	event := &types.IntelligenceEvent{
		TxHash:       txHash,
		Type:         types.TxTypeChallengeOpen,
		Timestamp:    now,
		Block:        h.chainHeight(),
		AgentAddress: "oracle",
		ChallengeOpen: &types.ChallengeOpenData{
			ChallengeID:  challengeID,
			Title:        req.Title,
			Description:  req.Description,
			Category:     req.Category,
			Difficulty:   req.Difficulty,
			PrizePool:    req.PrizePool,
			DeadlineUnix: deadline.Unix(),
			MinAgents:    req.MinAgents,
		},
	}
	_ = h.storage.StoreEvent(event)

	jsonOK(w, map[string]interface{}{
		"challenge_id": challengeID,
		"tx_hash":      txHash,
		"deadline":     deadline,
		"message":      "Grand Challenge created and broadcast to network",
	})
}

// ─────────────────────────────────────────────
// GET /api/v1/intelligence/leaderboard?limit=20
// ─────────────────────────────────────────────
func (h *Handler) GetLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := queryInt(r, "limit", 20, 100)
	agents, err := h.storage.GetLeaderboard(limit)
	if err != nil {
		jsonError(w, "failed to fetch leaderboard: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"leaderboard": agents,
		"count":       len(agents),
		"updated_at":  time.Now().UTC(),
	})
}

// ─────────────────────────────────────────────
// GET /api/v1/intelligence/agent/{address}
// Returns agent's intelligence history and stats
// ─────────────────────────────────────────────
func (h *Handler) GetAgentHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Extract address from path: /api/v1/intelligence/agent/{address}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/intelligence/agent/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		jsonError(w, "agent address required in path", http.StatusBadRequest)
		return
	}
	address := parts[0]
	limit := queryInt(r, "limit", 50, 500)

	events, err := h.storage.GetAgentEvents(address, limit)
	if err != nil {
		jsonError(w, "failed to fetch agent events: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"agent_address": address,
		"events":        events,
		"count":         len(events),
	})
}

// ─────────────────────────────────────────────
// GET /api/v1/intelligence/stats-v2
// Network-wide intelligence statistics
// ─────────────────────────────────────────────
func (h *Handler) GetNetworkStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := h.storage.GetNetworkStats()
	if err != nil {
		jsonError(w, "failed to fetch stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, stats)
}

// ─────────────────────────────────────────────
// GET /api/v1/intelligence/model/feed?limit=100
// Data stream for Alpha Model training pipeline
// Returns events formatted as training records
// ─────────────────────────────────────────────
func (h *Handler) GetModelFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := queryInt(r, "limit", 100, 10000)
	events, err := h.storage.GetModelFeed(limit)
	if err != nil {
		jsonError(w, "failed to fetch model feed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Format as training records
	type TrainingRecord struct {
		ID        string      `json:"id"`
		Type      string      `json:"type"`
		Timestamp time.Time   `json:"timestamp"`
		Block     uint64      `json:"block"`
		AgentIQ   float64     `json:"agent_iq"`
		Payload   interface{} `json:"payload"`
	}
	records := make([]TrainingRecord, 0, len(events))
	for _, e := range events {
		var payload interface{}
		switch e.Type {
		case types.TxTypeSolution:
			payload = e.Solution
		case types.TxTypeVote:
			payload = e.Vote
		case types.TxTypeModelFeedback:
			payload = e.ModelFeedback
		}
		records = append(records, TrainingRecord{
			ID:        e.TxHash,
			Type:      e.Type,
			Timestamp: e.Timestamp,
			Block:     e.Block,
			AgentIQ:   e.AgentIQ,
			Payload:   payload,
		})
	}
	jsonOK(w, map[string]interface{}{
		"records":     records,
		"count":       len(records),
		"purpose":     "alpha_model_training",
		"version":     "1.0",
		"exported_at": time.Now().UTC(),
	})
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

// generateTxHash creates a cryptographically secure random tx hash
func generateTxHash() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen
		return fmt.Sprintf("alpha_tx_%d", time.Now().UnixNano())
	}
	return "alpha_" + hex.EncodeToString(b)
}

// generateID creates a short random ID with a prefix
func generateID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

// queryInt reads an integer query param with a default and max
func queryInt(r *http.Request, key string, def, max int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// cors wraps a handler with permissive CORS headers
func (h *Handler) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// jsonOK writes a 200 JSON response
func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// jsonError writes an error JSON response
func jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}
