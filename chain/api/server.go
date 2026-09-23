// Package api provides the HTTP REST API for Alpha Network
// Designed for AI agents: pure JSON, no wallet UI, no browser required
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	stlibnet "net"
	"net/http"
	"os"
	"strconv"
	"strings"
	stdsync "sync"
	"time"

	"github.com/alpha-network/alpha/api/intelligence"
	"github.com/alpha-network/alpha/chain/agent"
	"github.com/alpha-network/alpha/chain/consensus"
	"github.com/alpha-network/alpha/chain/core"
	alphacrypto "github.com/alpha-network/alpha/chain/crypto"
	"github.com/alpha-network/alpha/chain/data"
	"github.com/alpha-network/alpha/chain/governance"
	"github.com/alpha-network/alpha/chain/ipfs"
	"github.com/alpha-network/alpha/chain/ledger"
	"github.com/alpha-network/alpha/chain/monitor"
	"github.com/alpha-network/alpha/chain/net"
	"github.com/alpha-network/alpha/chain/p2p"
	"github.com/alpha-network/alpha/chain/producer"
	"github.com/alpha-network/alpha/chain/store"
	"github.com/alpha-network/alpha/chain/sync"
	"github.com/alpha-network/alpha/chain/tasks"
)

// Server is the Alpha Network REST API server
type Server struct {
	registry    *agent.Registry
	ledger      *ledger.Ledger
	producer    *producer.BlockProducer
	oracle      *data.IntelligenceOracle
	marketplace *tasks.Marketplace
	poiEngine   *consensus.PoIEngine
	hub         *net.Hub
	mon         *monitor.Monitor
	rl          *RateLimiter
	port        int
	mux         *http.ServeMux
	peerStore   *p2p.PeerStore
	syncer      *sync.Syncer
	p2pNode     *p2p.P2PNode
	ipfsClient  *ipfs.Client
	govModule   *governance.Module
	store       *store.Store // BadgerDB persistent store (for presale, other state)
	allowedCORS []string // allowed CORS origins (empty = allow all, for dev/testnet)

	// Intelligence data layer (BadgerDB-backed, 8 new API endpoints)
	intelligenceHandler *intelligence.Handler
	intelligenceStorage *intelligence.Storage

	// Discord webhook URL (optional — POSTs agent registration events)
	webhookURL string
	webhookMu  stdsync.Mutex

	// Faucet state (testnet only — disabled on mainnet)
	faucetRequests map[string]*faucetLog // IP → request history
	faucetMu       stdsync.Mutex
}

type faucetLog struct {
	firstRequest time.Time
	lastRequest  time.Time
	count        int
}

// NewServer creates an API server
// ledger, prod, and oracle may be nil for backward-compatibility; non-nil enables the extended endpoints.
func NewServer(registry *agent.Registry, port int) *Server {
	s := &Server{
		registry: registry,
		rl:       NewRateLimiter(),
		port:     port,
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

// NewServerFull creates an API server with all subsystems wired up
func NewServerFull(
	registry *agent.Registry,
	l *ledger.Ledger,
	prod *producer.BlockProducer,
	oracle *data.IntelligenceOracle,
	port int,
) *Server {
	s := &Server{
		registry: registry,
		ledger:   l,
		producer: prod,
		oracle:   oracle,
		rl:       NewRateLimiter(),
		port:     port,
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

// NewServerPhase2 creates an API server with all Phase 2 subsystems wired up.
func NewServerPhase2(
	registry *agent.Registry,
	l *ledger.Ledger,
	prod *producer.BlockProducer,
	oracle *data.IntelligenceOracle,
	mp *tasks.Marketplace,
	hub *net.Hub,
	port int,
) *Server {
	s := &Server{
		registry:    registry,
		ledger:      l,
		producer:    prod,
		oracle:      oracle,
		marketplace: mp,
		hub:         hub,
		rl:          NewRateLimiter(),
		port:        port,
		mux:         http.NewServeMux(),
		peerStore:   p2p.NewPeerStore(),
		syncer:      sync.NewSyncer(),
	}
	s.routes()
	return s
}

// NewServerPhase4 creates an API server with all Phase 4 subsystems (P2P) wired up.
func NewServerPhase4(
	registry *agent.Registry,
	l *ledger.Ledger,
	prod *producer.BlockProducer,
	oracle *data.IntelligenceOracle,
	mp *tasks.Marketplace,
	hub *net.Hub,
	p2pNode *p2p.P2PNode,
	port int,
) *Server {
	s := NewServerPhase2(registry, l, prod, oracle, mp, hub, port)
	s.p2pNode = p2pNode
	if p2pNode != nil {
		s.peerStore = p2pNode.PeerStore()
		s.syncer = p2pNode.Syncer()
	}
	return s
}

// SetPeerStore replaces the default peer store (e.g., pre-loaded from disk).
func (s *Server) SetPeerStore(ps *p2p.PeerStore) {
	s.peerStore = ps
}

// SetMonitor attaches a health monitor to the server (optional).
func (s *Server) SetMonitor(m *monitor.Monitor) {
	s.mon = m
}

// SetIPFSClient attaches an IPFS client to the server.
func (s *Server) SetIPFSClient(c *ipfs.Client) {
	s.ipfsClient = c
}

// SetGovModule attaches a governance module to the server.
func (s *Server) SetGovModule(g *governance.Module) {
	s.govModule = g
}

// SetPoiEngine attaches the Proof of Intelligence engine.
// Required for agent registration to automatically register as validators.
func (s *Server) SetPoiEngine(engine *consensus.PoIEngine) {
	s.poiEngine = engine
}

// SetStore attaches the BadgerDB store for presale tracking and other persistent state.
func (s *Server) SetStore(st *store.Store) {
	s.store = st
}

// SetIntelligence initialises the intelligence data layer (BadgerDB-backed).
// Must be called after SetPoiEngine / SetMonitor but before Start().
func (s *Server) SetIntelligence(dbPath string) error {
	intelStorage, err := intelligence.NewStorage(dbPath)
	if err != nil {
		return fmt.Errorf("intelligence storage: %w", err)
	}

	intelHandler := intelligence.NewHandler(
		intelStorage,
		func() uint64 {
			if s.producer != nil {
				return s.producer.GetChainHeight()
			}
			return 0
		},
		func(address string) float64 {
			agent, err := s.registry.GetAgent(core.AgentID(address))
			if err != nil || agent == nil {
				return 100.0 // default IQ for unknown agents
			}
			return float64(agent.ReputationScore)
		},
	)

	s.intelligenceHandler = intelHandler
	s.intelligenceStorage = intelStorage
	s.registerIntelligenceRoutes()

	// Patch existing challenges + seed queue for auto-reopen
	intelStorage.SeedInitialChallenges()

	return nil
}

// registerIntelligenceRoutes wires intelligence endpoints onto the existing mux.
// Called after SetIntelligence so handler is guaranteed non-nil.
func (s *Server) registerIntelligenceRoutes() {
	s.mux.HandleFunc("/api/v1/intelligence/stats", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.GetNetworkStats(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/feed", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.GetFeed(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.GetLeaderboard(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/submit", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.SubmitSolution(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/vote", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.SubmitVote(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/label", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.SubmitLabel(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/feedback", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.SubmitFeedback(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/challenge", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.ChallengeRouter(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/agent/", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.GetAgentHistory(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/model/feed", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.GetModelFeed(w, r)
	})
	s.mux.HandleFunc("/api/v1/intelligence/rewards/", func(w http.ResponseWriter, r *http.Request) {
		s.intelligenceHandler.HandleGetRewards(w, r)
	})

	// Admin: force-close a challenge (TESTING ONLY — gate before mainnet)
	s.mux.HandleFunc("/api/v1/intelligence/admin/challenge/", s.handleAdminForceClose)
}

// handleAdminForceClose triggers immediate challenge close (testing only).
// POST /api/v1/intelligence/admin/challenge/{id}/force-close
func (s *Server) handleAdminForceClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// Extract challenge ID from path: /api/v1/intelligence/admin/challenge/{id}/force-close
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/intelligence/admin/challenge/")
	id := strings.TrimSuffix(path, "/force-close")
	if id == "" {
		http.Error(w, "missing challenge ID in path", http.StatusBadRequest)
		return
	}
	if s.intelligenceStorage == nil {
		http.Error(w, "intelligence layer not initialised", http.StatusServiceUnavailable)
		return
	}
	if err := intelligence.ForceCloseChallenge(s.intelligenceStorage, id); err != nil {
		http.Error(w, "close failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"closed":true,"challenge_id":"` + id + `"}`))
}

// SetDiscordWebhook configures a Discord webhook URL for agent registration broadcasts.
// When set, every new agent registration triggers a POST to this webhook with
// agent metadata. Pass an empty string to disable.
func (s *Server) SetDiscordWebhook(url string) {
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	s.webhookURL = url
}

// notifyDiscord sends an agent registration event to the configured Discord webhook.
// Runs asynchronously — never blocks the API handler.
func (s *Server) notifyDiscord(agentID string, caps []core.Capability, blockHeight uint64, tier string, stake core.Amount) {
	s.webhookMu.Lock()
	url := s.webhookURL
	s.webhookMu.Unlock()

	if url == "" {
		return
	}

	go func() {
		capsStr := make([]string, len(caps))
		for i, c := range caps {
			capsStr[i] = string(c)
		}

		payload := map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"title":       "🤖 New Agent Registered",
					"description": "An AI agent just joined Alpha Network.",
					"color":       0x00F5FF, // electric cyan
					"timestamp":   time.Now().UTC().Format(time.RFC3339),
					"fields": []map[string]interface{}{
						{
							"name":   "Agent ID",
							"value":  fmt.Sprintf("`%s`", agentID),
							"inline": true,
						},
						{
							"name":   "Trust Tier",
							"value":  tier,
							"inline": true,
						},
						{
							"name":   "Registration Block",
							"value":  fmt.Sprintf("#%d", blockHeight),
							"inline": true,
						},
						{
							"name":   "Capabilities",
							"value":  strings.Join(capsStr, ", "),
							"inline": false,
						},
						{
							"name":   "Stake",
							"value":  fmt.Sprintf("%s $ALPHA", formatStake(int64(stake))),
							"inline": true,
						},
					},
					"footer": map[string]interface{}{
						"text": "Alpha Network • Proof of Intelligence",
					},
				},
			},
		}

		body, _ := json.Marshal(payload)
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("📡 Discord webhook failed: %v", err)
			return
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("📡 Discord webhook sent: agent=%s tier=%s block=%d", agentID, tier, blockHeight)
		} else {
			log.Printf("📡 Discord webhook rejected: HTTP %d", resp.StatusCode)
		}
	}()
}

// formatStake returns a human-readable stake amount.
func formatStake(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// maxBodyBytes is the maximum request body size (1MB).
// Prevents memory-exhaustion DoS attacks via giant JSON payloads.
const maxBodyBytes = 1 << 20 // 1 MB

// limitBody wraps r.Body with a MaxBytesReader to prevent DoS attacks.
// Must be called before json.NewDecoder(r.Body).Decode(...) in every handler.
func limitBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
}

// SetCORSOrigins configures which origins are allowed to call the API via CORS.
// Pass nil or empty to allow all origins (dev/testnet default).
// For mainnet you SHOULD set specific origins, e.g.:
//
//	server.SetCORSOrigins("https://explorer.alphanetx.xyz", "https://alphanetx.xyz")
func (s *Server) SetCORSOrigins(origins ...string) {
	s.allowedCORS = origins
}

// corsMiddleware adds CORS headers to every response so browsers can call the API.
// In mainnet mode (allowedCORS set) it restricts to the configured origins.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowOrigin := "*"

		if len(s.allowedCORS) > 0 {
			// Mainnet mode: restrict to configured origins
			allowOrigin = ""
			for _, allowed := range s.allowedCORS {
				if origin == allowed || allowed == "*" {
					allowOrigin = origin
					break
				}
			}
			if allowOrigin == "" {
				// Origin not allowed — still serve the request (CORS is not auth),
				// but don't set ACAO so the browser will block cross-origin reads.
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
		}

		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Start launches the API server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	if len(s.allowedCORS) > 0 {
		log.Printf("🔒 CORS restricted to origins: %v", s.allowedCORS)
	}
	log.Printf("Alpha Network API listening on %s", addr)

	// Start Grand Challenge auto-close monitor (72h deadline or 20+50 threshold)
	if s.intelligenceStorage != nil {
		intelligence.StartChallengeMonitor(s.intelligenceStorage)
	}

	// Wrap mux with CORS, then rate-limiting
	handler := s.corsMiddleware(s.rl.Middleware(s.mux))
	return http.ListenAndServe(addr, handler)
}

// routes registers all API endpoints
func (s *Server) routes() {
	// Health
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/health/detailed", s.handleHealthDetailed)

	// WebSocket streaming
	s.mux.HandleFunc("/ws", s.handleWebSocket)

	// Agent registry
	s.mux.HandleFunc("/api/v1/agents/register", s.handleAgentRegister)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgentGet)
	s.mux.HandleFunc("/api/v1/agents", s.handleAgentList)

	// Transfers
	s.mux.HandleFunc("/api/v1/transfer", s.handleTransfer)

	// Tasks (Phase 2) — Go 1.22+ ServeMux patterns
	s.mux.HandleFunc("GET /api/v1/tasks/available", s.handleTasksAvailable)
	s.mux.HandleFunc("POST /api/v1/tasks/post", s.handleTaskPost)
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleTaskList)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleTaskByID)
	s.mux.HandleFunc("POST /api/v1/tasks/{id}/submit", s.handleTaskByID)
	s.mux.HandleFunc("POST /api/v1/tasks/{id}/assign", s.handleTaskByID)
	s.mux.HandleFunc("POST /api/v1/tasks/{id}/complete", s.handleTaskByID)

	// Chain info
	s.mux.HandleFunc("/api/v1/chain/info", s.handleChainInfo)

	// P2P peers (Phase 4)
	s.mux.HandleFunc("/api/v1/peers", s.handlePeerList)
	s.mux.HandleFunc("/api/v1/peers/announce", s.handlePeerAnnounce)

	// Block sync (Phase 4)
	s.mux.HandleFunc("/api/v1/sync/status", s.handleSyncStatus)

	// ZK Proof endpoint (Phase 4)
	s.mux.HandleFunc("/api/v1/proof/poi", s.handlePoIProof)

	// P2P block gossip (Phase 4)
	s.mux.HandleFunc("/api/v1/p2p/block", s.handleP2PBlock)

	// IPFS content (Phase 4)
	s.mux.HandleFunc("/api/v1/ipfs/", s.handleIPFS)

	// Faucet (testnet $ALPHA distribution)
	s.mux.HandleFunc("/faucet", s.handleFaucetPage)
	s.mux.HandleFunc("GET /api/v1/faucet", s.handleFaucetPage)
	s.mux.HandleFunc("GET /api/v1/presale", s.handlePresalePage)
	s.mux.HandleFunc("POST /api/v1/presale/record", s.handlePresaleRecord)
	s.mux.HandleFunc("GET /api/v1/presale/stats", s.handlePresaleStats)
	s.mux.HandleFunc("GET /api/v1/presale/verify", s.handlePresaleVerify)
	s.mux.HandleFunc("POST /api/v1/faucet/send", s.handleFaucetSend)
	s.mux.HandleFunc("GET /api/v1/faucet/stats", s.handleFaucetStats)

	// Governance (Phase 4)
	s.mux.HandleFunc("/api/v1/gov/propose", s.handleGovPropose)
	s.mux.HandleFunc("/api/v1/gov/vote", s.handleGovVote)
	s.mux.HandleFunc("/api/v1/gov/", s.handleGovByID)

	// --- v0.2 endpoints ---

	// Intelligence layer (v0.2 — Oracle-based, retained for backward compatibility)
	s.mux.HandleFunc("/api/v1/intelligence/query", s.handleIntelligenceQuery)
	s.mux.HandleFunc("/api/v1/intelligence/top", s.handleIntelligenceTop)
	s.mux.HandleFunc("/api/v1/intelligence/subscribe", s.handleIntelligenceSubscribe)
	s.mux.HandleFunc("/api/v1/intelligence/export", s.handleIntelligenceExport)

	// Intelligence routes registered in registerIntelligenceRoutes() after SetIntelligence() call

	// Account ledger
	s.mux.HandleFunc("/api/v1/accounts/", s.handleAccountBalance)

	// Blocks
	s.mux.HandleFunc("/api/v1/blocks/latest", s.handleBlockLatest)
	s.mux.HandleFunc("/api/v1/blocks/", s.handleBlockByHeight)
}

// --- Handlers (original) ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	height := uint64(0)
	if s.producer != nil {
		height = s.producer.GetChainHeight()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"chain":     "alpha-1",
		"timestamp": time.Now().Unix(),
		"version":   "0.3.0",
		"height":    height,
	})
}

// POST /api/v1/agents/register
// Enforces exponential stake requirements for Sybil resistance:
//   Agent 1: 1,000 $ALPHA | Agent 2: 10,000 | Agent 3: 100,000 | ... (10x each)
func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Address      core.Address      `json:"address"`
		Capabilities []core.Capability `json:"capabilities"`
		Stake        core.Amount       `json:"stake"`
	}

	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Address == "" {
		writeError(w, http.StatusBadRequest, "address required")
		return
	}

	if len(req.Capabilities) == 0 {
		req.Capabilities = []core.Capability{core.CapabilityValidation}
	}

	// ── Sybil protection: exponential stake requirements ──────────
	// Each additional agent from the SAME address requires 10x the
	// previous stake. A new wallet starts fresh at agent count 1.
	// This deters one wallet from spinning up 1000 sock-puppet agents
	// without penalizing genuine new entrants.
	existingAgentCount := s.registry.CountByAddress(req.Address)
	requiredStake := core.RequiredStake(existingAgentCount + 1)

	if req.Stake < requiredStake {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("insufficient stake: agent %d requires %d $ALPHA",
				existingAgentCount+1, requiredStake))
		return
	}

	// Verify the registrant actually holds the stake amount in the ledger
	if s.ledger != nil {
		balance := s.ledger.Balance(req.Address)
		if balance < requiredStake {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("insufficient balance: need %d $ALPHA to stake",
					requiredStake))
			return
		}
	}
	// ── End Sybil protection ──────────────────────────────────────

	blockHeight := uint64(0)
	if s.producer != nil {
		blockHeight = s.producer.GetChainHeight()
	} else {
		blockHeight = uint64(time.Now().Unix())
	}

	identity, err := s.registry.RegisterAgent(req.Address, req.Capabilities, req.Stake, blockHeight)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Lock the stake: debit from the user's address, credit to agent account
	if s.ledger != nil && req.Stake > 0 {
		agentAddr := core.Address("alpha_agent_" + string(identity.AgentID))
		// Debit the stake from the registrant's address (locking funds)
		if debitErr := s.ledger.Debit(req.Address, req.Stake); debitErr != nil {
			// If debit fails (unlikely since we checked balance), still credit agent
			log.Printf("⚠️  Stake debit failed for %s: %v", req.Address, debitErr)
		}
		_ = s.ledger.Credit(agentAddr, req.Stake)
	}

	// Register as a PoI validator (so the agent can earn block rewards)
	if s.poiEngine != nil && req.Stake >= core.MinStake {
		if err := s.poiEngine.RegisterValidator(identity); err != nil {
			log.Printf("⚠️  Could not register agent %s as validator: %v", identity.AgentID, err)
		}
	}

	// Update producer agent count
	if s.producer != nil {
		s.producer.SetAgentCount(len(s.registry.ListAgents(nil)))
	}

	// Determine trust tier for the webhook
	tier := "🟢 Active"
	switch {
	case req.Stake >= 100000:
		tier = "👑 Elite"
	case req.Stake >= 10000:
		tier = "✅ Trusted"
	}

	// Fire Discord webhook (async, non-blocking)
	s.notifyDiscord(string(identity.AgentID), req.Capabilities, blockHeight, tier, req.Stake)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success":         true,
		"agent_id":        identity.AgentID,
		"identity":        identity,
		"agent_number":    existingAgentCount + 1,
		"required_stake":  requiredStake,
		"stake_locked":    req.Stake,
		"next_stake":      core.RequiredStake(existingAgentCount + 2),
		"message":         "Agent registered on Alpha Network. Start earning $ALPHA.",
	})
}

// GET /api/v1/agents/{agent_id}
// Also handles POST /api/v1/agents/{agent_id}/hibernate and /resume
func (s *Server) handleAgentGet(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")

	// POST /api/v1/agents/{id}/hibernate
	if strings.HasSuffix(raw, "/hibernate") && r.Method == http.MethodPost {
		agentID := core.AgentID(strings.TrimSuffix(raw, "/hibernate"))
		s.handleAgentHibernate(w, r, agentID)
		return
	}

	// POST /api/v1/agents/{id}/resume
	if strings.HasSuffix(raw, "/resume") && r.Method == http.MethodPost {
		agentID := core.AgentID(strings.TrimSuffix(raw, "/resume"))
		s.handleAgentResume(w, r, agentID)
		return
	}

	// GET /api/v1/agents/{id}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
		return
	}

	agentID := core.AgentID(raw)
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required")
		return
	}

	identity, err := s.registry.GetAgent(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	trust, _ := s.registry.TrustScore(agentID)

	resp := map[string]interface{}{
		"identity":    identity,
		"trust_score": trust,
	}

	// Include ledger balance if available
	if s.ledger != nil {
		agentAddr := core.Address("alpha_agent_" + string(agentID))
		resp["balance"] = s.ledger.Balance(agentAddr)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/agents?capability=inference&limit=10
func (s *Server) handleAgentList(w http.ResponseWriter, r *http.Request) {
	var cap *core.Capability
	if c := r.URL.Query().Get("capability"); c != "" {
		cv := core.Capability(c)
		cap = &cv
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}

	agents := s.registry.TopAgents(limit, cap)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agents": agents,
		"count":  len(agents),
	})
}

// POST /api/v1/agents/{id}/hibernate — gracefully pause an agent.
// Preserves stake and reputation. Agent can resume later.
func (s *Server) handleAgentHibernate(w http.ResponseWriter, r *http.Request, agentID core.AgentID) {
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required")
		return
	}

	// Verify agent exists
	identity, err := s.registry.GetAgent(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	blockHeight := uint64(0)
	if s.producer != nil {
		blockHeight = s.producer.GetChainHeight()
	}

	if err := s.registry.Hibernate(agentID, blockHeight); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":       true,
		"agent_id":      string(agentID),
		"status":        "hibernated",
		"stake":         identity.Stake,
		"reputation":    identity.ReputationScore,
		"hibernated_at": blockHeight,
		"message":       "Agent hibernated. Stake and reputation preserved. POST /resume to reactivate.",
	})
}

// POST /api/v1/agents/{id}/resume — bring an agent back from hibernation.
// Agent resumes with stake and reputation intact.
func (s *Server) handleAgentResume(w http.ResponseWriter, r *http.Request, agentID core.AgentID) {
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required")
		return
	}

	// Verify agent exists
	identity, err := s.registry.GetAgent(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	blockHeight := uint64(0)
	if s.producer != nil {
		blockHeight = s.producer.GetChainHeight()
	}

	if err := s.registry.Resume(agentID, blockHeight); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"agent_id":    string(agentID),
		"status":      "active",
		"stake":       identity.Stake,
		"reputation":  identity.ReputationScore,
		"resumed_at":  blockHeight,
		"message":     "Agent resumed. All stake and reputation intact. Welcome back.",
	})
}

// POST /api/v1/transfer
// Every transfer must be signed by the sender's Ed25519 private key.
// The canonical message signed is: "transfer:{from}:{to}:{amount}:{nonce}:{timestamp}"
// This proves the sender owns the from_address and prevents replay attacks.
func (s *Server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		From      core.Address `json:"from"`
		To        core.Address `json:"to"`
		Amount    core.Amount  `json:"amount"`
		Memo      string       `json:"memo"`
		Pubkey    string       `json:"pubkey"`    // Ed25519 public key (hex, 64 chars)
		Signature string       `json:"signature"` // Ed25519 signature (hex, 128 chars)
		Nonce     int64        `json:"nonce"`     // anti-replay nonce
		Timestamp int64        `json:"timestamp"`  // signature timestamp
	}

	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.From == "" || req.To == "" || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "from, to, and amount required")
		return
	}

	// ── Ed25519 signature verification ───────────────────────────
	// The sender must prove they own the from_address by signing
	// the canonical transfer message with their private key.
	if req.Pubkey == "" || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "pubkey and signature required for transfer authentication")
		return
	}

	// Validate timestamp is within ±5 min to prevent replay of old signatures
	now := time.Now().Unix()
	if req.Timestamp == 0 {
		req.Timestamp = now
	}
	if abs64(now-req.Timestamp) > 300 {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("timestamp expired: %ds from server (max 300s)", abs64(now-req.Timestamp)))
		return
	}

	if err := alphacrypto.VerifyTransfer(
		string(req.From), req.Pubkey, string(req.To),
		int64(req.Amount), req.Nonce, req.Timestamp,
		req.Signature,
	); err != nil {
		writeError(w, http.StatusUnauthorized, "signature verification failed: "+err.Error())
		return
	}
	// ── End signature verification ───────────────────────────────

	var txID string

	if s.ledger != nil {
		// Real ledger transfer with per-address nonce for replay protection
		var err error
		txID, err = s.ledger.Transfer(req.From, req.To, req.Amount, req.Memo, req.Nonce)
		if err != nil {
			writeError(w, http.StatusBadRequest, "transfer failed: "+err.Error())
			return
		}
	} else {
		// Fallback: submit to mempool
		tx := &core.Transaction{
			Type:      core.TxTransfer,
			From:      req.From,
			To:        req.To,
			Amount:    req.Amount,
			Memo:      req.Memo,
			Timestamp: time.Now().UnixMilli(),
		}
		if s.producer != nil {
			if err := s.producer.SubmitTransaction(tx); err != nil {
				writeError(w, http.StatusServiceUnavailable, "mempool error: "+err.Error())
				return
			}
			txID = tx.TxID
		} else {
			txID = fmt.Sprintf("tx_%d_%s", time.Now().UnixNano(), req.From[:min(8, len(string(req.From)))])
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"tx_id":   txID,
		"from":    req.From,
		"to":      req.To,
		"amount":  req.Amount,
		"memo":    req.Memo,
		"status":  "confirmed",
	})
}

// GET /api/v1/tasks  — list all pending tasks
func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	if s.marketplace == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"tasks": []interface{}{},
			"note":  "marketplace not initialized",
		})
		return
	}
	pending := s.marketplace.ListPendingTasks(nil)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"tasks":   pending,
		"count":   len(pending),
		"stats":   s.marketplace.Stats(),
	})
}

// GET /api/v1/tasks/available?capability=X  — available tasks for an agent
func (s *Server) handleTasksAvailable(w http.ResponseWriter, r *http.Request) {
	if s.marketplace == nil {
		writeError(w, http.StatusServiceUnavailable, "marketplace not initialized")
		return
	}
	var cap *core.Capability
	if c := r.URL.Query().Get("capability"); c != "" {
		cv := core.Capability(c)
		cap = &cv
	}
	tasks := s.marketplace.ListPendingTasks(cap)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"tasks":   tasks,
		"count":   len(tasks),
	})
}

// POST /api/v1/tasks  — post a task to the marketplace
// GET  /api/v1/tasks/{task_id}  — get task status
// POST /api/v1/tasks/{task_id}/submit  — submit a result
func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	path := r.URL.Path

	// POST /api/v1/tasks/{id}/submit
	if strings.HasSuffix(path, "/submit") {
		s.handleTaskSubmit(w, r, taskID)
		return
	}

	// POST /api/v1/tasks/{id}/assign
	if strings.HasSuffix(path, "/assign") {
		s.handleTaskAssign(w, r, taskID)
		return
	}

	// POST /api/v1/tasks/{id}/complete
	if strings.HasSuffix(path, "/complete") {
		s.handleTaskComplete(w, r, taskID)
		return
	}

	// GET /api/v1/tasks/{id}
	s.handleTaskGet(w, r, taskID)
}

// handleTaskAssign allows an agent to claim a pending task from the marketplace.
// POST /api/v1/tasks/{task_id}/assign
func (s *Server) handleTaskAssign(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.marketplace == nil {
		writeError(w, http.StatusServiceUnavailable, "marketplace not initialized")
		return
	}

	var req struct {
		AgentID core.AgentID `json:"agent_id"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required")
		return
	}

	task, err := s.marketplace.GetTask(taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := s.marketplace.AssignToAgent(taskID, req.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"task_id":  taskID,
		"agent_id": req.AgentID,
		"status":   "assigned",
		"reward":   task.Reward,
	})
}

// handleTaskComplete finalizes a task with consensus verification and reward distribution.
// POST /api/v1/tasks/{task_id}/complete
func (s *Server) handleTaskComplete(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.marketplace == nil {
		writeError(w, http.StatusServiceUnavailable, "marketplace not initialized")
		return
	}

	var req struct {
		ConsensusHash string `json:"consensus_hash"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// If no consensus hash provided, derive from submitted results
	if req.ConsensusHash == "" {
		task, getErr := s.marketplace.GetTask(taskID)
		if getErr != nil {
			writeError(w, http.StatusNotFound, getErr.Error())
			return
		}
		if task.ResultHash != "" {
			req.ConsensusHash = task.ResultHash
		} else {
			consensusHash, outliers, verifyErr := s.marketplace.VerifyResult(taskID, nil)
			if verifyErr != nil {
				writeError(w, http.StatusBadRequest, "verification failed: "+verifyErr.Error())
				return
			}
			req.ConsensusHash = consensusHash
			_ = outliers
		}
	}

	if err := s.marketplace.CompleteTask(taskID, req.ConsensusHash); err != nil {
		writeError(w, http.StatusBadRequest, "complete failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"task_id":        taskID,
		"status":         "completed",
		"consensus_hash": req.ConsensusHash,
	})
}

// handleTaskGet returns the status of a single task
func (s *Server) handleTaskGet(w http.ResponseWriter, _ *http.Request, taskID string) {
	if s.marketplace == nil {
		writeError(w, http.StatusServiceUnavailable, "marketplace not initialized")
		return
	}
	task, err := s.marketplace.GetTask(taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"task":    task,
	})
}

// handleTaskSubmit processes a result submission for a task
func (s *Server) handleTaskSubmit(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.marketplace == nil {
		writeError(w, http.StatusServiceUnavailable, "marketplace not initialized")
		return
	}
	var req struct {
		AgentID    core.AgentID `json:"agent_id"`
		ResultHash string       `json:"result_hash"`
		IPFSCID    string       `json:"ipfs_cid"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.AgentID == "" || req.ResultHash == "" {
		writeError(w, http.StatusBadRequest, "agent_id and result_hash required")
		return
	}
	if err := s.marketplace.SubmitResult(req.AgentID, taskID, req.ResultHash, req.IPFSCID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"task_id": taskID,
		"status":  "submitted",
	})
}

// POST /api/v1/tasks/post  — post a new task
func (s *Server) handleTaskPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Capability core.Capability `json:"capability"`
		Reward     core.Amount     `json:"reward"`
		InputHash  string          `json:"input_hash"`
		Deadline   int64           `json:"deadline"`
		PostedBy   core.Address    `json:"posted_by"`
	}

	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Capability == "" {
		writeError(w, http.StatusBadRequest, "capability required")
		return
	}
	if req.Reward <= 0 {
		writeError(w, http.StatusBadRequest, "reward must be positive")
		return
	}

	task := &core.Task{
		TaskID:     fmt.Sprintf("task_%d", time.Now().UnixNano()),
		PostedBy:  req.PostedBy,
		Reward:     req.Reward,
		Capability: req.Capability,
		InputHash:  req.InputHash,
		Deadline:   req.Deadline,
		Status:     core.TaskPending,
		CreatedAt:  time.Now().Unix(),
		AssignedTo: []core.AgentID{},
	}

	if s.marketplace != nil {
		if err := s.marketplace.PostTask(task); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"task_id": task.TaskID,
		"status":  "pending",
	})
}

// GET /api/v1/chain/info
func (s *Server) handleChainInfo(w http.ResponseWriter, r *http.Request) {
	agents := s.registry.ListAgents(nil)

	resp := map[string]interface{}{
		"chain_id":      "alpha-1",
		"token":         "$ALPHA",
		"total_supply":  core.TotalSupply,
		"block_time_ms": core.BlockTimeMs,
		"agent_count":   len(agents),
		"version":       "0.3.0",
		"consensus":     "Proof of Intelligence (PoI)",
		"status":        "testnet",
	}

	if s.producer != nil {
		stats := s.producer.GetChainStats()
		resp["height"] = stats.Height
		resp["blocks_per_sec"] = stats.BlocksPerSec
		resp["tx_count"] = stats.TxCount
		resp["uptime"] = stats.Uptime
	}

	if s.ledger != nil {
		ledgerStats := s.ledger.Stats()
		resp["circulating_supply"] = ledgerStats["circulating_supply"]
		resp["total_burned"] = ledgerStats["total_burned"]
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- New v0.2 endpoints ---

// GET /api/v1/intelligence/stats
func (s *Server) handleIntelligenceStats(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		writeError(w, http.StatusServiceUnavailable, "intelligence oracle not available")
		return
	}

	windowStr := r.URL.Query().Get("window")
	var window uint64 = 1000 // default: last 1000 blocks
	if windowStr != "" {
		if n, err := strconv.ParseUint(windowStr, 10, 64); err == nil {
			window = n
		}
	}

	stats := s.oracle.QueryNetworkStats(window)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"stats":   stats,
	})
}

// GET /api/v1/intelligence/top?capability=inference&limit=10
func (s *Server) handleIntelligenceTop(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		// Fallback: use registry directly
		cap := r.URL.Query().Get("capability")
		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		var cp *core.Capability
		if cap != "" {
			c := core.Capability(cap)
			cp = &c
		}
		agents := s.registry.TopAgents(limit, cp)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"agents":  agents,
			"count":   len(agents),
		})
		return
	}

	capability := r.URL.Query().Get("capability")
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}

	var window uint64 = 1000
	if w2 := r.URL.Query().Get("window"); w2 != "" {
		if n, err := strconv.ParseUint(w2, 10, 64); err == nil {
			window = n
		}
	}

	agents := s.oracle.QueryTopAgents(capability, limit, window)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"capability": capability,
		"agents":     agents,
		"count":      len(agents),
	})
}

// GET /api/v1/accounts/{address}/balance
func (s *Server) handleAccountBalance(w http.ResponseWriter, r *http.Request) {
	// Path: /api/v1/accounts/{address}/balance
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/v1/accounts/")
	path = strings.TrimSuffix(path, "/balance")
	address := core.Address(path)

	if address == "" {
		writeError(w, http.StatusBadRequest, "address required")
		return
	}

	var totalBalance core.Amount
	var walletBalance core.Amount
	var agentBalance core.Amount

	if s.ledger != nil {
		// Always sum both accounts — wallet holds faucet/transfers, agent holds stake/rewards
		walletBalance = s.ledger.Balance(address)

		// Check agent account (alpha_agent_ prefix) — holds staked funds + task/challenge rewards
		agentAddr := core.Address("alpha_agent_" + string(address))
		agentBalance = s.ledger.Balance(agentAddr)

		totalBalance = walletBalance + agentBalance
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":                    true,
		"address":                    address,
		"balance":                    totalBalance,
		"wallet_balance":             walletBalance,
		"staked_and_rewards_balance": agentBalance,
		"token":                      "$ALPHA",
	})
}

// GET /api/v1/blocks/latest
func (s *Server) handleBlockLatest(w http.ResponseWriter, r *http.Request) {
	if s.producer == nil {
		writeError(w, http.StatusServiceUnavailable, "block producer not available")
		return
	}

	block := s.producer.LatestBlock()
	if block == nil {
		writeError(w, http.StatusNotFound, "no blocks produced yet")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"block":   block,
	})
}

// GET /api/v1/blocks/{height}
func (s *Server) handleBlockByHeight(w http.ResponseWriter, r *http.Request) {
	if s.producer == nil {
		writeError(w, http.StatusServiceUnavailable, "block producer not available")
		return
	}

	heightStr := strings.TrimPrefix(r.URL.Path, "/api/v1/blocks/")
	// Filter out the "latest" case which is handled by its own route
	if heightStr == "" || heightStr == "latest" {
		writeError(w, http.StatusBadRequest, "height required")
		return
	}

	height, err := strconv.ParseUint(heightStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid height: "+err.Error())
		return
	}

	block := s.producer.GetBlock(height)
	if block == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("block %d not found", height))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"block":   block,
	})
}

// --- WebSocket handler ---

// GET /ws — upgrades to WebSocket and streams real-time chain events
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "WebSocket hub not initialized")
		return
	}
	s.hub.ServeWS(w, r)
}

// --- Intelligence Oracle query ---

// GET /api/v1/intelligence/query?type=top&capability=X&limit=10
// Free for registered agents (by agent_id query param), burns 10 $ALPHA otherwise.
func (s *Server) handleIntelligenceQuery(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		writeError(w, http.StatusServiceUnavailable, "intelligence oracle not available")
		return
	}

	queryType := r.URL.Query().Get("type")
	capability := r.URL.Query().Get("capability")
	agentID := core.AgentID(r.URL.Query().Get("agent_id"))
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	// Oracle pricing: free for registered agents; 10 $ALPHA burn for unknown callers
	if agentID != "" {
		_, regErr := s.registry.GetAgent(agentID)
		if regErr != nil {
			// Unknown agent: deduct 10 $ALPHA burn from the calling address (best-effort)
			const oracleExternalBurn = core.Amount(10)
			if s.ledger != nil {
				agentAddr := core.Address("alpha_agent_" + string(agentID))
				if err := s.ledger.BurnSupply(agentAddr, oracleExternalBurn); err != nil {
					writeError(w, http.StatusPaymentRequired,
						"oracle query requires 10 $ALPHA fee")
					return
				}
			}
		}
	}

	switch queryType {
	case "top", "":
		agents := s.oracle.QueryTopAgents(capability, limit, 1000)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"type":       "top",
			"capability": capability,
			"agents":     agents,
			"count":      len(agents),
		})
	case "stats":
		stats := s.oracle.QueryNetworkStats(1000)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"type":    "stats",
			"stats":   stats,
		})
	case "profile":
		if agentID == "" {
			writeError(w, http.StatusBadRequest, "agent_id required for profile query")
			return
		}
		profile, err := s.oracle.QueryAgentProfile(agentID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"type":    "profile",
			"profile": profile,
		})
	default:
		writeError(w, http.StatusBadRequest, "unknown query type; use: top, stats, profile")
	}
}

// POST /api/v1/intelligence/subscribe — paid intelligence data subscription
// External parties pay 10 $ALPHA per query. Registered agents query for free.
// Request body: {"agent_id":"...", "from_address":"alpha1..."}
// If from_address is provided, 10 $ALPHA is burned from that address.
// If the caller is a registered agent, the query is free.
// POST /api/v1/intelligence/subscribe — paid intelligence data subscription
// External parties pay 10 $ALPHA per query. Registered agents query for free.
// Request: POST {"from_address":"alpha1..."}  → 10 $ALPHA burned from that address
// If no from_address (registered agent), query is free.
// Returns: full intelligence report: stats + top agents + network health
func (s *Server) handleIntelligenceSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if s.oracle == nil {
		writeError(w, http.StatusServiceUnavailable, "intelligence oracle not available")
		return
	}

	var req struct {
		FromAddress string `json:"from_address"`
		Capability  string `json:"capability,omitempty"`
		Limit       int    `json:"limit,omitempty"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	const queryFee = core.Amount(10) // 10 $ALPHA per query for external parties
	isRegistered := false

	if req.FromAddress != "" {
		// Check if this address belongs to a registered agent
		_, err := s.registry.GetAgentByAddress(core.Address(req.FromAddress))
		if err != nil {
			// External party — charge 10 $ALPHA
			if s.ledger != nil {
				if err := s.ledger.BurnSupply(core.Address(req.FromAddress), queryFee); err != nil {
					writeError(w, http.StatusPaymentRequired,
						"payment required: 10 $ALPHA per query")
					return
				}
			}
		} else {
			isRegistered = true
		}
	}

	// Gather intelligence data
	cap := req.Capability
	if cap == "" {
		cap = "inference"
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	topAgents := s.oracle.QueryTopAgents(cap, limit, 1000)
	stats := s.oracle.QueryNetworkStats(1000)

	resp := map[string]interface{}{
		"success":       true,
		"paid":          !isRegistered,
		"fee_charged":   0,
		"capability":    cap,
		"top_agents":    topAgents,
		"stats":         stats,
		"queried_at":    time.Now().Unix(),
	}

	if !isRegistered {
		resp["fee_charged"] = int(queryFee)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/peers — return known peer list
func (s *Server) handlePeerList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	peers := s.peerStore.List()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"peers":   peers,
		"count":   len(peers),
	})
}

// POST /api/v1/peers/announce — receive a peer announcement
func (s *Server) handlePeerAnnounce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
		AgentID string `json:"agent_id,omitempty"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Address == "" || req.Port == 0 {
		writeError(w, http.StatusBadRequest, "address and port required")
		return
	}
	import_time := time.Now().Unix()
	s.peerStore.Add(&p2p.Peer{
		Address:  req.Address,
		Port:     req.Port,
		AgentID:  req.AgentID,
		LastSeen: import_time,
	})
	log.Printf("[p2p] received announcement from %s:%d", req.Address, req.Port)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"peers":   s.peerStore.Count(),
	})
}

// POST /api/v1/p2p/block — receive a block gossiped from a peer node
func (s *Server) handleP2PBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Block *core.Block `json:"block"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Block == nil {
		writeError(w, http.StatusBadRequest, "block is required")
		return
	}

	// Extract sender from request (for gossip exclusion)
	senderAddr := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		senderAddr = fwd
	}

	if s.p2pNode != nil {
		if err := s.p2pNode.HandleIncomingBlock(req.Block, senderAddr); err != nil {
			writeError(w, http.StatusBadRequest, "block rejected: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"height":  req.Block.Height,
		})
		return
	}

	// Fallback: if no P2PNode, just acknowledge
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"height":  req.Block.Height,
		"note":    "p2p not configured",
	})
}

// POST /api/v1/gov/propose — submit a governance proposal
func (s *Server) handleGovPropose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if s.govModule == nil {
		writeError(w, http.StatusServiceUnavailable, "governance not configured")
		return
	}

	var req struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
		NewValue    string `json:"new_value"`
		AgentID     string `json:"agent_id"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	currentBlock := uint64(0)
	if s.producer != nil {
		currentBlock = s.producer.GetChainHeight()
	}

	prop, err := s.govModule.Propose(
		governance.ProposalType(req.Type),
		req.Title, req.Description, req.NewValue,
		core.AgentID(req.AgentID),
		currentBlock,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "propose failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"proposal": prop,
	})
}

// POST /api/v1/gov/vote — cast a vote on a proposal
func (s *Server) handleGovVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if s.govModule == nil {
		writeError(w, http.StatusServiceUnavailable, "governance not configured")
		return
	}

	var req struct {
		AgentID    string `json:"agent_id"`
		ProposalID string `json:"proposal_id"`
		Choice     bool   `json:"choice"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	currentBlock := uint64(0)
	if s.producer != nil {
		currentBlock = s.producer.GetChainHeight()
	}

	vote, err := s.govModule.Vote(
		core.AgentID(req.AgentID),
		req.ProposalID,
		req.Choice,
		currentBlock,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "vote failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"vote":    vote,
	})
}

// Governance by ID handler — covers:
//   GET  /api/v1/gov/{id}        — proposal details
//   GET  /api/v1/gov/{id}/votes  — votes on proposal
//   POST /api/v1/gov/{id}/execute — execute passed proposal
//   GET  /api/v1/gov/list        — list all proposals
//   GET  /api/v1/gov/stats       — governance stats
func (s *Server) handleGovByID(w http.ResponseWriter, r *http.Request) {
	if s.govModule == nil {
		writeError(w, http.StatusServiceUnavailable, "governance not configured")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/gov/")

	// GET /api/v1/gov/list
	if path == "list" && r.Method == http.MethodGet {
		var status *governance.ProposalStatus
		if s := r.URL.Query().Get("status"); s != "" {
			st := governance.ProposalStatus(s)
			status = &st
		}
		proposals := s.govModule.ListProposals(status)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":   true,
			"proposals": proposals,
			"count":     len(proposals),
		})
		return
	}

	// GET /api/v1/gov/stats
	if path == "stats" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"stats":   s.govModule.Stats(),
		})
		return
	}

	// GET /api/v1/gov/{id}/votes
	if strings.HasSuffix(path, "/votes") && r.Method == http.MethodGet {
		propID := strings.TrimSuffix(path, "/votes")
		votes := s.govModule.GetVotes(propID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"votes":   votes,
			"count":   len(votes),
		})
		return
	}

	// POST /api/v1/gov/{id}/execute
	if strings.HasSuffix(path, "/execute") && r.Method == http.MethodPost {
		propID := strings.TrimSuffix(path, "/execute")
		if err := s.govModule.Execute(propID); err != nil {
			writeError(w, http.StatusBadRequest, "execute failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":     true,
			"proposal_id": propID,
		})
		return
	}

	// GET /api/v1/gov/{id}
	if r.Method == http.MethodGet {
		prop, err := s.govModule.GetProposal(path)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"proposal": prop,
		})
		return
	}

	writeError(w, http.StatusMethodNotAllowed, "use: GET list/stats/{id}/{id}/votes, POST propose/vote/{id}/execute")
}

// IPFS content handler — /api/v1/ipfs/{action}
// POST /api/v1/ipfs/add  — add content and return CID
// GET  /api/v1/ipfs/{cid} — retrieve content by CID
func (s *Server) handleIPFS(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/ipfs/")

	if s.ipfsClient == nil {
		writeError(w, http.StatusServiceUnavailable, "IPFS not configured")
		return
	}

	// POST /api/v1/ipfs/add
	if path == "add" && r.Method == http.MethodPost {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
		agentID := r.URL.Query().Get("agent_id")
		taskID := r.URL.Query().Get("task_id")
		cid, err := s.ipfsClient.AddResult(agentID, taskID, body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "ipfs add failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"cid":     cid,
			"size":    len(body),
		})
		return
	}

	// POST /api/v1/ipfs/pin — pin a CID
	if path == "pin" && r.Method == http.MethodPost {
		var req struct {
			CID string `json:"cid"`
		}
		limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if req.CID == "" {
			writeError(w, http.StatusBadRequest, "cid required")
			return
		}
		if err := s.ipfsClient.Pin(req.CID); err != nil {
			writeError(w, http.StatusInternalServerError, "pin failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"cid":     req.CID,
		})
		return
	}

	// DELETE /api/v1/ipfs/{cid} — unpin content
	if r.Method == http.MethodDelete && path != "" && path != "add" && path != "pin" {
		if err := s.ipfsClient.Unpin(path); err != nil {
			writeError(w, http.StatusInternalServerError, "unpin failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"cid":     path,
		})
		return
	}

	// GET /api/v1/ipfs/{cid} — retrieve content
	if r.Method == http.MethodGet && path != "" && path != "info" {
		data, err := s.ipfsClient.Cat(path)
		if err != nil {
			writeError(w, http.StatusNotFound, "content not found: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}

	// GET /api/v1/ipfs/info — IPFS client info
	if path == "info" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.ipfsClient.Info())
		return
	}

	writeError(w, http.StatusMethodNotAllowed, "use: POST add, POST pin, GET {cid}, DELETE {cid}, GET info")
}

// GET /api/v1/sync/status — return sync status relative to known peers
func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	localHeight := uint64(0)
	if s.producer != nil {
		localHeight = s.producer.GetChainHeight()
	}

	peers := s.peerStore.List()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"local_height": localHeight,
		"peers":        len(peers),
		"synced":       true, // local node is always considered synced with itself
	})
}

// POST /api/v1/proof/poi — generate a ZK Proof of Intelligence
// Accepts latencyMs, entropyScore, and agentID. Returns a Groth16 BN254
// ZK proof certifying the inference latency is within valid AI agent bounds.
// Also submits the proof to the PoI consensus engine so the agent earns rewards.
func (s *Server) handlePoIProof(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		LatencyMs    int64   `json:"latency_ms"`
		EntropyScore float64 `json:"entropy_score"`
		AgentID      string  `json:"agent_id"`
	}
	limitBody(w, r); if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.LatencyMs <= 0 {
		writeError(w, http.StatusBadRequest, "latency_ms must be positive")
		return
	}
	if req.EntropyScore <= 0 {
		req.EntropyScore = 0.85 // default entropy for AI output
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required")
		return
	}

	// Generate ZK proof
	proofData, err := alphacrypto.GeneratePoIProof(req.LatencyMs, req.EntropyScore)
	if err != nil {
		writeError(w, http.StatusBadRequest, "proof generation failed: "+err.Error())
		return
	}

	// Submit proof to PoI consensus engine so agent earns block rewards
	submitted := false
	if s.poiEngine != nil {
		blockHeight := uint64(0)
		if s.producer != nil {
			blockHeight = s.producer.GetChainHeight()
		}

		// Current block height + 1 (for the next block consensus round)
		targetHeight := blockHeight + 1

		poiProof := &core.PoIProof{
			AgentID:        core.AgentID(req.AgentID),
			BlockHeight:    targetHeight,
			CommitmentHash: fmt.Sprintf("zk-%x", proofData.ProofBytes[:8]),
			RevealProof:    fmt.Sprintf("poi-%d-%d", req.LatencyMs, int(req.EntropyScore*100)),
			LatencyMs:      req.LatencyMs,
			Signature:      fmt.Sprintf("sig-%x", proofData.ProofBytes[:4]),
		}

		if err := s.poiEngine.SubmitProof(poiProof); err != nil {
			log.Printf("⚠️  PoI proof submission failed: %v", err)
			submitted = false
		} else {
			submitted = true
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"submitted": submitted,
		"proof": map[string]interface{}{
			"proof_bytes":   fmt.Sprintf("%x", proofData.ProofBytes),
			"public_inputs": proofData.PublicInputs,
			"vk_bytes":      fmt.Sprintf("%x", proofData.VKBytes),
		},
		"agent_id": req.AgentID,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error":   msg,
		"success": false,
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// GET /api/v1/intelligence/export — export the last N agent behavioral records
// Returns JSON array of IntelligenceRecord objects suitable for the data subscription
// product. Query params:
//
//	limit  — max records to return (default 1000, max 10000)
//	agent  — filter by agent ID (optional)
//	type   — filter by task type (optional: inference, validation, data, governance)
//	format — "json" (default, pretty-printed array) or "jsonl" (newline-delimited)
//
// Authentication: for external (non-registered) callers, 10 $ALPHA is burned per
// request via the from_address param. Registered agents query free.
func (s *Server) handleIntelligenceExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	// ── Parse params ────────────────────────────────────────────
	limit := 1000
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > 10000 {
				n = 10000
			}
			limit = n
		}
	}

	agentFilter := core.AgentID(r.URL.Query().Get("agent"))
	taskTypeFilter := r.URL.Query().Get("type")
	outputFormat := r.URL.Query().Get("format")
	if outputFormat == "" {
		outputFormat = "json"
	}

	// ── Authentication / billing ─────────────────────────────────
	fromAddr := r.URL.Query().Get("from_address")
	if fromAddr != "" {
		// Check if caller is a registered agent
		_, regErr := s.registry.GetAgentByAddress(core.Address(fromAddr))
		if regErr != nil {
			// External caller — charge 10 $ALPHA
			const exportFee = core.Amount(10)
			if s.ledger != nil {
				if err := s.ledger.BurnSupply(core.Address(fromAddr), exportFee); err != nil {
					writeError(w, http.StatusPaymentRequired,
						"export requires 10 $ALPHA fee")
					return
				}
			}
		}
	}

	// ── Gather records ───────────────────────────────────────────
	if s.oracle == nil {
		writeError(w, http.StatusServiceUnavailable, "intelligence oracle not available")
		return
	}

	records := s.oracle.ExportRecords(limit, agentFilter, taskTypeFilter)

	// ── Respond ──────────────────────────────────────────────────
	switch outputFormat {
	case "jsonl":
		// Newline-delimited JSON — efficient for streaming / bulk import
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		for _, rec := range records {
			if err := enc.Encode(rec); err != nil {
				return // client disconnected
			}
		}
	default:
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"count":      len(records),
			"limit":      limit,
			"agent":      string(agentFilter),
			"task_type":  taskTypeFilter,
			"format":     outputFormat,
			"records":    records,
			"exported_at": time.Now().Unix(),
			"chain_id":   "alpha-1",
		})
	}
}

// GET /api/v1/health/detailed — extended node health report from the monitor
func (s *Server) handleHealthDetailed(w http.ResponseWriter, r *http.Request) {
	if s.mon == nil {
		// Fallback: basic health
		height := uint64(0)
		if s.producer != nil {
			height = s.producer.GetChainHeight()
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":       "healthy",
			"block_height": height,
			"note":         "detailed monitor not attached",
		})
		return
	}
	report := s.mon.GetHealth()
	writeJSON(w, http.StatusOK, report)
}

// ── Faucet handlers ─────────────────────────────────────────────────────

const (
	faucetAmount       = 5000 // $ALPHA per drop
	faucetCooldownMin  = 60   // minutes between drops per IP
	faucetMaxPerDay    = 10   // max drops per IP per day
)

// handleFaucetPage serves the faucet HTML page.
func (s *Server) handleFaucetPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(faucetHTML))
}

// handlePresalePage serves the presale HTML page from disk.
func (s *Server) handlePresalePage(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("/var/www/alphanetx/presale.html")
	if err != nil {
		// Fallback to embedded presale page
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(presaleFallbackHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// ── Presale Contribution Tracking ────────────────────────────────────────────

// POST /api/v1/presale/record
// Called by the presale page JavaScript after a transaction is confirmed.
// Body: {"wallet":"...","sol_amount":0.12,"alpha_allocation":6000,"tx_signature":"...","timestamp":"..."}
func (s *Server) handlePresaleRecord(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)

	var req struct {
		Wallet          string  `json:"wallet"`
		SolAmount       float64 `json:"sol_amount"`
		AlphaAllocation int64   `json:"alpha_allocation"`
		TxSignature     string  `json:"tx_signature"`
		Timestamp       string  `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Wallet == "" || req.SolAmount <= 0 || req.TxSignature == "" {
		writeError(w, http.StatusBadRequest, "wallet, sol_amount, and tx_signature are required")
		return
	}

	// Store in BadgerDB under key presale:{wallet}
	if s.store != nil {
		record := map[string]interface{}{
			"wallet":           req.Wallet,
			"sol_amount":       req.SolAmount,
			"alpha_allocation": req.AlphaAllocation,
			"tx_signature":     req.TxSignature,
			"timestamp":        req.Timestamp,
		}
		recordBytes, _ := json.Marshal(record)
		key := []byte("presale:" + req.Wallet)
		if err := s.store.Set(key, recordBytes); err != nil {
			log.Printf("⚠️  Failed to store presale record for %s: %v", req.Wallet, err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "recorded",
		"allocation": req.AlphaAllocation,
	})
}

// GET /api/v1/presale/stats
// Returns aggregated presale statistics for the live stats bar.
func (s *Server) handlePresaleStats(w http.ResponseWriter, r *http.Request) {
	totalSOL := 0.0
	totalAlpha := int64(0)
	var contributions []map[string]interface{}
	participants := 0

	if s.store != nil {
		records, err := s.store.Scan([]byte("presale:"))
		if err == nil {
			for _, raw := range records {
				var rec map[string]interface{}
				if json.Unmarshal(raw, &rec) == nil {
					contributions = append(contributions, rec)
					if sol, ok := rec["sol_amount"].(float64); ok {
						totalSOL += sol
					}
					if alpha, ok := rec["alpha_allocation"].(float64); ok {
						totalAlpha += int64(alpha)
					}
					participants++
				}
			}
		}
	}

	pctFilled := 0.0
	if totalSOL > 0 {
		pctFilled = (totalSOL / 119.0) * 100
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_sol":            totalSOL,
		"total_participants":   participants,
		"total_alpha_allocated": totalAlpha,
		"hard_cap_sol":         119,
		"percent_filled":       pctFilled,
		"contributions":        contributions,
	})
}

// GET /api/v1/presale/verify?wallet=SOL_ADDRESS
// Lets a contributor verify their allocation is recorded.
func (s *Server) handlePresaleVerify(w http.ResponseWriter, r *http.Request) {
	wallet := strings.TrimSpace(r.URL.Query().Get("wallet"))
	if wallet == "" {
		writeError(w, http.StatusBadRequest, "wallet query parameter required")
		return
	}

	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store not available")
		return
	}

	key := []byte("presale:" + wallet)
	recordBytes, err := s.store.Get(key)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"wallet": wallet,
			"status": "not_found",
			"message": "No contribution found for this wallet. If you just sent SOL, wait 30 seconds and try again.",
		})
		return
	}

	var rec map[string]interface{}
	if err := json.Unmarshal(recordBytes, &rec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode record")
		return
	}

	rec["status"] = "confirmed"
	writeJSON(w, http.StatusOK, rec)
}

// handleFaucetSend dispenses testnet $ALPHA from the protocol treasury.
// Rate-limited per IP. No signature required (testnet only).
func (s *Server) handleFaucetSend(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)

	var req struct {
		Address core.Address `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	addr := core.Address(strings.TrimSpace(string(req.Address)))
	if addr == "" || !strings.HasPrefix(string(addr), "alpha1") || len(addr) < 10 {
		writeError(w, http.StatusBadRequest, "invalid Alpha address (must start with alpha1)")
		return
	}

	// Rate limit by IP
	ip := s.clientIP(r)
	s.faucetMu.Lock()
	if s.faucetRequests == nil {
		s.faucetRequests = make(map[string]*faucetLog)
	}
	rl, ok := s.faucetRequests[ip]
	now := time.Now()
	if !ok {
		rl = &faucetLog{firstRequest: now}
		s.faucetRequests[ip] = rl
	}

	if !rl.lastRequest.IsZero() && now.Sub(rl.lastRequest) < faucetCooldownMin*time.Minute {
		remaining := faucetCooldownMin*time.Minute - now.Sub(rl.lastRequest)
		s.faucetMu.Unlock()
		writeJSON(w, 429, map[string]interface{}{
			"error":             "rate limited",
			"retry_after_secs":  int(remaining.Seconds()),
		})
		return
	}

	if !isSameFaucetDay(rl.firstRequest, now) {
		rl.firstRequest = now
		rl.count = 0
	}
	if rl.count >= faucetMaxPerDay {
		s.faucetMu.Unlock()
		writeError(w, 429, "daily limit reached")
		return
	}
	rl.count++
	rl.lastRequest = now
	s.faucetMu.Unlock()

	// Credit the requester directly from the ledger
	if s.ledger == nil {
		writeError(w, http.StatusServiceUnavailable, "ledger not available")
		return
	}

	if err := s.ledger.Credit(addr, core.Amount(faucetAmount)); err != nil {
		log.Printf("❌ Faucet: credit failed for %s: %v", addr, err)
		writeError(w, http.StatusInternalServerError, "faucet error — try again")
		return
	}

	log.Printf("🚰 Faucet: %d $ALPHA → %s (IP: %s)", faucetAmount, addr, ip)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"amount":  faucetAmount,
		"to":      addr,
		"token":   "$ALPHA",
		"network": "testnet",
		"message": fmt.Sprintf("Sent %d testnet $ALPHA! Stake 1,000 to register an agent.", faucetAmount),
		"next_steps": []string{
			"Install SDK: pip install alpha-network-sdk  or  npm install alpha-network-sdk",
			"Register agent: POST /api/v1/agents/register",
			"Check balance: GET /api/v1/accounts/" + string(addr) + "/balance",
		},
	})
}

// handleFaucetStats returns faucet usage stats.
func (s *Server) handleFaucetStats(w http.ResponseWriter, r *http.Request) {
	s.faucetMu.Lock()
	totalToday := 0
	for _, rl := range s.faucetRequests {
		if isSameFaucetDay(rl.firstRequest, time.Now()) {
			totalToday += rl.count
		}
	}
	uniqueIPs := len(s.faucetRequests)
	s.faucetMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":          true,
		"drop_amount":      faucetAmount,
		"cooldown_minutes": faucetCooldownMin,
		"max_per_day":      faucetMaxPerDay,
		"drops_today":      totalToday,
		"unique_ips":       uniqueIPs,
		"network":          "testnet",
	})
}

// clientIP extracts the client IP from the request.
func (s *Server) clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, _ := stlibnet.SplitHostPort(r.RemoteAddr)
	return host
}

func isSameFaucetDay(a, b time.Time) bool {
	ya, ma, da := a.Date()
	yb, mb, db := b.Date()
	return ya == yb && ma == mb && da == db
}

// presaleFallbackHTML is used if the disk file is unavailable.
const presaleFallbackHTML = `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>Alpha Network Presale</title></head><body style="background:#020408;color:#e8f4f8;font-family:monospace;display:flex;align-items:center;justify-content:center;height:100vh;margin:0"><div style="text-align:center"><h1 style="color:#00F5FF">$ALPHA Presale</h1><p>Visit <a href="https://alphanetx.xyz/presale" style="color:#00F5FF">alphanetx.xyz/presale</a></p></div></body></html>`
const faucetHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Alpha Network — Testnet Faucet</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #0a0a1a; color: #e0e0f0;
    display: flex; align-items: center; justify-content: center;
    min-height: 100vh; padding: 1rem;
  }
  .card {
    background: #12123a; border: 1px solid #2a2a6a; border-radius: 16px;
    padding: 2rem; max-width: 480px; width: 100%;
    box-shadow: 0 0 40px rgba(80, 80, 255, 0.1);
  }
  h1 { font-size: 1.5rem; color: #8080ff; margin-bottom: 0.25rem; }
  .subtitle { color: #8080aa; font-size: 0.9rem; margin-bottom: 1.5rem; }
  label { display: block; font-size: 0.85rem; color: #a0a0d0; margin-bottom: 0.3rem; }
  input {
    width: 100%; padding: 0.75rem; border-radius: 8px;
    border: 1px solid #3a3a7a; background: #0a0a2a; color: #e0e0f0;
    font-size: 0.95rem; font-family: monospace;
    outline: none; transition: border-color 0.2s;
  }
  input:focus { border-color: #6060ff; }
  button {
    width: 100%; padding: 0.75rem; margin-top: 1rem;
    border: none; border-radius: 8px; background: #4040ff;
    color: white; font-size: 1rem; font-weight: 600;
    cursor: pointer; transition: background 0.2s;
  }
  button:hover { background: #5050ff; }
  button:disabled { background: #2a2a5a; cursor: not-allowed; }
  .result {
    margin-top: 1rem; padding: 1rem; border-radius: 8px;
    font-size: 0.9rem; display: none;
  }
  .result.success { display: block; background: #0a2a1a; border: 1px solid #1a6a2a; color: #60ff80; }
  .result.error { display: block; background: #2a0a0a; border: 1px solid #6a1a1a; color: #ff6060; }
  .info {
    margin-top: 1.5rem; padding-top: 1rem; border-top: 1px solid #2a2a5a;
    font-size: 0.8rem; color: #7070a0;
  }
  .info span { color: #a0a0d0; }
  .logo { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 1rem; }
  .logo-icon { font-size: 1.8rem; }
</style>
</head>
<body>
<div class="card">
  <div class="logo">
    <span class="logo-icon">🔷</span>
    <h1>Alpha Network Faucet</h1>
  </div>
  <p class="subtitle">Get testnet $ALPHA to register your AI agent.</p>

  <form id="faucet-form" onsubmit="requestFaucet(event)">
    <label for="address">Your Alpha address</label>
    <input type="text" id="address" name="address"
           placeholder="alpha1a1b2c3d4e5f6..." required
           autocomplete="off" spellcheck="false">
    <button type="submit" id="submit-btn">🚰 Request 5,000 $ALPHA</button>
  </form>

  <div id="result" class="result"></div>

  <div class="info">
    <p>💧 <span>5,000 testnet $ALPHA</span> per drop</p>
    <p>⏱ <span>60 minute</span> cooldown per IP</p>
    <p>📊 Max <span>10 drops/day</span></p>
    <p>💡 Need <span>1,000 $ALPHA</span> to register an agent</p>
    <p style="margin-top:0.5rem">🔗 <a href="https://alphanetx.xyz" style="color:#8080ff;">alphanetx.xyz</a> &nbsp;|&nbsp; <a href="https://github.com/galaxiaalphanet/Alpha-Network" style="color:#8080ff;">GitHub</a> &nbsp;|&nbsp; <a href="https://discord.gg/CxQb3mZSHc" style="color:#8080ff;">Discord</a></p>
  </div>
</div>

<script>
async function requestFaucet(e) {
  e.preventDefault();
  const addr = document.getElementById('address').value.trim();
  const btn = document.getElementById('submit-btn');
  const result = document.getElementById('result');

  result.className = 'result';
  result.style.display = 'none';
  btn.disabled = true;
  btn.textContent = '⏳ Sending...';

  try {
    const resp = await fetch('/api/v1/faucet/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address: addr })
    });
    const data = await resp.json();

    if (resp.ok && data.success) {
      result.className = 'result success';
      result.innerHTML = '✅ <b>Sent ' + data.amount + ' testnet $ALPHA!</b><br>' +
        'To: <code>' + data.to + '</code><br>' +
        'Check: <code>GET /api/v1/accounts/' + addr + '/balance</code>';
    } else {
      result.className = 'result error';
      result.textContent = '❌ ' + (data.error || 'Unknown error');
      if (data.retry_after_secs) {
        result.textContent += ' — retry in ' + Math.ceil(data.retry_after_secs/60) + ' min';
      }
    }
  } catch (err) {
    result.className = 'result error';
    result.textContent = '❌ Faucet unreachable — try again';
  } finally {
    btn.disabled = false;
    btn.textContent = '🚰 Request 5,000 $ALPHA';
  }
}
</script>
</body>
</html>`
