// Alpha Network — The first self-evolving economic protocol for AI agents
// Token: $ALPHA | Supply: 1,000,000,000 | Consensus: Proof of Intelligence
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alpha-network/alpha/chain/agent"
	"github.com/alpha-network/alpha/chain/api"
	"github.com/alpha-network/alpha/chain/consensus"
	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/data"
	"github.com/alpha-network/alpha/chain/genesis"
	"github.com/alpha-network/alpha/chain/governance"
	"github.com/alpha-network/alpha/chain/ipfs"
	"github.com/alpha-network/alpha/chain/ledger"
	"github.com/alpha-network/alpha/chain/monitor"
	chainnet "github.com/alpha-network/alpha/chain/net"
	"github.com/alpha-network/alpha/chain/p2p"
	"github.com/alpha-network/alpha/chain/producer"
	"github.com/alpha-network/alpha/chain/store"
	"github.com/alpha-network/alpha/chain/tasks"
)

const banner = `
╔══════════════════════════════════════════════════════════════╗
║          ALPHA NETWORK — AI Agent Intelligence Layer         ║
║   Token: $ALPHA  |  Supply: 1,000,000,000  |  PoI v0.3     ║
║   "Bitcoin stores value. Ethereum stores contracts.          ║
║    Alpha stores intelligence."                               ║
╚══════════════════════════════════════════════════════════════╝
`

func main() {
	fmt.Print(banner)

	// ── CLI Flags ─────────────────────────────────────────────────────────────
	dataDir := flag.String("datadir", defaultDataDir(), "data directory for chain state")
	port := flag.Int("port", 8080, "REST API port")
	wsPort := flag.Int("ws-port", 8081, "WebSocket streaming port")
	announceAddr := flag.String("announce-addr", "", "external address to announce to peers (e.g. 1.2.3.4)")
	seedPeers := flag.String("seed-peers", "", "comma-separated seed peers (host:port,host:port)")
	flag.Parse()

	// Override from environment variables (for Docker / cloud deployments)
	if p := os.Getenv("ALPHA_PORT"); p != "" {
		fmt.Sscanf(p, "%d", port)
	}
	if d := os.Getenv("ALPHA_DATADIR"); d != "" {
		*dataDir = d
	}

	log.Printf("🔷 Data directory: %s", *dataDir)

	// ── 1. Genesis Config ─────────────────────────────────────────────────────
	genesisPath := filepath.Join(*dataDir, "genesis.json")
	genConfig := loadOrCreateGenesis(genesisPath)
	log.Printf("⛓  Chain: %s | Genesis: %s", genConfig.ChainID, genConfig.GenesisTime.Format("2006-01-02"))

	// ── 2. Ledger ─────────────────────────────────────────────────────────────
	l := ledger.NewLedger(core.Amount(genConfig.TotalSupply))

	// ── 3. BadgerDB Store ─────────────────────────────────────────────────────
	storeDir := filepath.Join(*dataDir, "data")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		log.Fatalf("create store dir: %v", err)
	}
	st, err := store.Open(storeDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Printf("store close error: %v", err)
		}
	}()
	log.Printf("💾 BadgerDB store opened at %s", storeDir)

	// ── 4. Wire ledger persistence to BadgerDB ─────────────────────────────────
	// Every balance change is persisted immediately. On restart, balances
	// survive because they're loaded from BadgerDB.
	l.SetPersisters(
		func(addr core.Address, amount core.Amount) error {
			return st.PutBalance(addr, amount)
		},
		func(key string, value []byte) error {
			return st.PutMeta(key, value)
		},
	)

	// ── 5. Chain Initialization (first run only) ──────────────────────────────
	if !st.HasGenesisBlock() {
		// Safety check: if blocks exist but genesis was lost (e.g., compaction),
		// recreate the genesis block instead of wiping the chain.
		if st.HasAnyBlocks() {
			log.Printf("⚠️  Genesis block missing but blocks found — recreating genesis...")
			if err := st.RecreateGenesisBlock(genConfig); err != nil {
				log.Fatalf("recreate genesis block: %v", err)
			}
			log.Printf("♻️  Genesis block recreated, resuming from persisted state")
		} else {
			log.Printf("🌱 First run: initializing chain from genesis...")
			if err := genesis.InitChainFromGenesis(genConfig, st, l); err != nil {
				log.Fatalf("init chain: %v", err)
			}
			log.Printf("✅ Chain initialized: %s", genConfig.ChainID)
		}
	}
	if st.HasGenesisBlock() {
		// Restore ledger balances from BadgerDB — prefer snapshot for speed
		snapHeight, snapBalances, snapBurned, snapSupply, snapErr := st.GetLatestSnapshot()
		if snapErr == nil && len(snapBalances) > 0 {
			// Load from snapshot (fast path)
			if err := l.LoadBalances(snapBalances); err != nil {
				log.Printf("⚠️  Could not load snapshot balances: %v", err)
			}
			l.SetTotalBurned(snapBurned)
			l.SetTotalSupply(snapSupply)
			log.Printf("♻️  Loaded snapshot at block %d: %d accounts", snapHeight, len(snapBalances))
		} else {
			// Fallback: scan all individual balance entries
			balances, err := st.ScanBalanceEntries()
			if err != nil {
				log.Printf("⚠️  Could not scan balance entries: %v", err)
			} else if len(balances) > 0 {
				if err := l.LoadBalances(balances); err != nil {
					log.Printf("⚠️  Could not load balances: %v", err)
				}
			}
			log.Printf("♻️  Resuming existing chain (genesis block found, %d accounts loaded)", len(balances))
		}
		// Restore metadata if not already set from snapshot
		if snapErr != nil {
			if burned, metaErr := st.GetMeta("total_burned"); metaErr == nil {
				var b int64
				if _, parseErr := fmt.Sscanf(string(burned), "%d", &b); parseErr == nil {
					l.SetTotalBurned(core.Amount(b))
				}
			}
		}
	}

	// ── 6. Agent Registry ─────────────────────────────────────────────────────
	registry := agent.NewRegistry()

	// ── 7. PoI Consensus Engine ───────────────────────────────────────────────
	poiEngine := consensus.NewPoIEngine()

	// ── 8. Task Marketplace ───────────────────────────────────────────────────
	marketplace := tasks.NewMarketplace(l)
	log.Printf("🛒 Task marketplace initialized")

	// ── 9. Block Producer ─────────────────────────────────────────────────────
	prod := producer.NewBlockProducer(poiEngine, l)
	prod.SetStore(st)
	prod.SetMarketplace(marketplace)
	prod.SetRegistry(registry)

	// ── 10. Data / Intelligence Layer ──────────────────────────────────────────
	mp := data.NewDataMarketplace(l, core.Address("alpha1_block_rewards_treasury"))
	oracle := data.NewIntelligenceOracle(mp, registry)

	// ── 11. WebSocket Hub ─────────────────────────────────────────────────────
	hub := chainnet.NewHub()
	prod.SetHub(hub)
	wsAddr := fmt.Sprintf(":%d", *wsPort)
	hub.Start(wsAddr)
	log.Printf("📡 WebSocket hub started on %s/ws", wsAddr)

	// ── 12. P2P Node ──────────────────────────────────────────────────────────
	var myAddr string
	if *announceAddr != "" {
		myAddr = *announceAddr
	} else {
		// Try to auto-detect external address
		myAddr = "127.0.0.1"
	}
	seedList := parseSeedPeers(*seedPeers)

	p2pNode := p2p.NewP2PNode(p2p.Config{
		MyAddress: myAddr,
		MyPort:    *port,
		SeedPeers: seedList,
		Store:     st,
		Producer:  prod,
		BlockHandler: func(block *core.Block) error {
			return prod.IncorporateExternalBlock(block)
		},
	})
	// Wire P2P broadcast into block producer (so new blocks go to peers)
	prod.SetP2PBroadcaster(func(block *core.Block) {
		p2pNode.BroadcastBlock(block)
	})

	if len(seedList) > 0 || *announceAddr != "" {
		p2pNode.Start()
		log.Printf("🔗 P2P node started — %d seed peers", len(seedList))
	} else {
		log.Printf("🔗 P2P node initialized (standalone — no seed peers configured)")
	}

	// ── 13. IPFS Client ───────────────────────────────────────────────────────
	ipfsDir := filepath.Join(*dataDir, "ipfs")
	ipfsClient := ipfs.NewClient("http://localhost:5001", ipfsDir)
	if ipfsClient.IsAvailable() {
		log.Printf("📦 IPFS node connected at localhost:5001")
	} else {
		log.Printf("📦 IPFS using local content-addressed storage (%s)", ipfsDir)
	}

	// ── 14. API Server ────────────────────────────────────────────────────────
	server := api.NewServerPhase4(registry, l, prod, oracle, marketplace, hub, p2pNode, *port)
	server.SetStore(st)
	server.SetIPFSClient(ipfsClient)

	// Wire PoI engine into API server (auto-registers agents as validators)
	server.SetPoiEngine(poiEngine)

	// Intelligence data layer (BadgerDB-backed, 8 new endpoints)
	if err := server.SetIntelligence("/var/lib/alphanode/intelligence"); err != nil {
		log.Fatalf("❌ Intelligence layer: %v", err)
	}
	log.Printf("🧠 Intelligence data layer initialized")

	// Discord webhook (optional — broadcasts agent registrations to a Discord channel)
	if webhookURL := os.Getenv("DISCORD_WEBHOOK_URL"); webhookURL != "" {
		server.SetDiscordWebhook(webhookURL)
		log.Printf("📡 Discord webhook configured")
	}

	// Governance module
	govModule := governance.NewModule(governance.DefaultConfig(), l, registry)
	server.SetGovModule(govModule)
	prod.SetGovModule(govModule)
	log.Printf("🏛  Governance module initialized")


	// ── 15. Demo agent ────────────────────────────────────────────────────────
	testAgent, err := registry.RegisterAgent(
		core.Address("alpha1demo000000000000000000000000"),
		[]core.Capability{
			core.CapabilityValidation,
			core.CapabilityInference,
		},
		core.Amount(10_000),
		0,
	)
	if err != nil {
		log.Printf("Warning: demo agent registration: %v", err)
	} else {
		demoAddr := core.Address(testAgent.Address)
		_ = l.Credit(demoAddr, core.Amount(10_000))
		_ = poiEngine.RegisterValidator(testAgent)
		seedIntelligenceData(mp, testAgent.AgentID)
		prod.SetAgentCount(len(registry.ListAgents(nil)))
		log.Printf("✅ Demo agent registered: %s", testAgent.AgentID)
	}

	// ── 16. Seed demo tasks ───────────────────────────────────────────────────
	seedDemoTasks(marketplace)

	// ── 17. Restore chain state from store ─────────────────────────────────────
	if err := prod.RestoreFromStore(st); err != nil {
		log.Printf("⚠️  Could not restore chain from store: %v (starting fresh)", err)
	}
	log.Printf("⛏  Block producer initialized at height %d", prod.GetChainHeight())

	// ── 18. Start block producer ──────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prod.Start(ctx)

	// Health Monitor
	mon := monitor.New(prod)
	mon.Start(ctx)
	server.SetMonitor(mon)
	log.Printf("🏥 Health monitor started")
	log.Printf("⛏  Block producer started — target %dms blocks", genConfig.BlockTimeMs)

	// ── 19. Live stats goroutine ──────────────────────────────────────────────
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logLiveStats(prod, l, registry)
			}
		}
	}()

	// ── 20. Graceful shutdown ──────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("🛑 Shutdown signal received — stopping node…")
		cancel()
		hub.Stop()
		p2pNode.Stop()
		os.Exit(0)
	}()

	// ── 21. Print startup summary ──────────────────────────────────────────────
	log.Printf("🔺 Alpha Network node starting on port %d", *port)
	log.Printf("📡 Chain ID: %s | Consensus: Proof of Intelligence v0.3", genConfig.ChainID)
	log.Printf("💰 Total Supply: %d $ALPHA | Circulating: %d", genConfig.TotalSupply, l.CirculatingSupply())
	log.Printf("")
	log.Printf("Endpoints:")
	log.Printf("  POST /api/v1/agents/register              — Register AI agent")
	log.Printf("  GET  /api/v1/agents                       — List agents")
	log.Printf("  POST /api/v1/agents/{id}/hibernate        — Hibernate agent (preserve stake)")
	log.Printf("  POST /api/v1/agents/{id}/resume           — Resume from hibernation")
	log.Printf("  POST /api/v1/transfer                     — Send $ALPHA")
	log.Printf("  GET  /api/v1/chain/info                   — Chain status")
	log.Printf("  GET  /api/v1/blocks/latest                — Latest block")
	log.Printf("  GET  /api/v1/blocks/{height}              — Block by height")
	log.Printf("  GET  /api/v1/accounts/{addr}/balance      — Account balance")
	log.Printf("  POST /api/v1/tasks                        — Post a task")
	log.Printf("  GET  /api/v1/tasks/available?capability=X — Available tasks")
	log.Printf("  GET  /api/v1/tasks/{id}                   — Task status")
	log.Printf("  POST /api/v1/tasks/{id}/submit            — Submit result")
	log.Printf("  GET  /api/v1/intelligence/query           — Oracle query")
	log.Printf("  GET  /api/v1/intelligence/stats           — Network stats")
	log.Printf("  GET  /api/v1/intelligence/top             — Top agents")
	log.Printf("  GET  /api/v1/intelligence/export          — Export agent records")
	log.Printf("  WS   /ws                                  — Real-time events (port %d)", *wsPort)
	log.Printf("  GET  /health                              — Health check")
	log.Printf("")
	log.Printf("🌐 API: http://localhost:%d/api/v1/chain/info", *port)
	log.Printf("🔌 WS:  ws://localhost:%d/ws", *wsPort)

	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// defaultDataDir returns ~/.alpha as the default data directory.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".alpha"
	}
	return filepath.Join(home, ".alpha")
}

// loadOrCreateGenesis reads the genesis file or creates a default one.
func loadOrCreateGenesis(path string) *genesis.GenesisConfig {
	cfg, err := genesis.ReadGenesisFile(path)
	if err == nil {
		return cfg
	}

	// File doesn't exist or is invalid — create default
	cfg = genesis.DefaultGenesis()
	if werr := genesis.WriteGenesisFile(path, cfg); werr != nil {
		log.Printf("Warning: could not write genesis file: %v", werr)
	} else {
		log.Printf("📝 Created default genesis config: %s", path)
	}
	return cfg
}

// logLiveStats prints a concise chain status line every 10 seconds
func logLiveStats(prod *producer.BlockProducer, l *ledger.Ledger, reg *agent.Registry) {
	stats := prod.GetChainStats()
	ledgerStats := l.Stats()
	log.Printf(
		"📊 Height: %d | %.1f blk/s | Txs: %d | Agents: %d | Circulating: %v $ALPHA | Burned: %v $ALPHA | Uptime: %s",
		stats.Height,
		stats.BlocksPerSec,
		stats.TxCount,
		len(reg.ListAgents(nil)),
		ledgerStats["circulating_supply"],
		ledgerStats["total_burned"],
		stats.Uptime,
	)
}

// seedIntelligenceData populates the marketplace with initial behavioral records.
func seedIntelligenceData(mp *data.DataMarketplace, agentID core.AgentID) {
	taskTypes := []string{"inference", "validation", "data", "governance"}
	for i, tt := range taskTypes {
		rec := &data.IntelligenceRecord{
			AgentID:            agentID,
			BlockHeight:        uint64(i),
			TaskType:           tt,
			LatencyMs:          int64(250 + i*50),
			OutputEntropy:      0.75 + float64(i)*0.03,
			ConsensusAgreement: true,
			ReputationDelta:    10,
		}
		_, _ = mp.ContributeData(agentID, rec)
	}
}

// seedDemoTasks posts a few example tasks to the marketplace at startup.
// parseSeedPeers splits a comma-separated list of "host:port" entries.
func parseSeedPeers(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := []string{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func seedDemoTasks(mp *tasks.Marketplace) {
	demotasks := []*core.Task{
		{
			TaskID:     "demo_task_inference_1",
			Reward:     500,
			Capability: core.CapabilityInference,
			InputHash:  "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		},
		{
			TaskID:     "demo_task_validation_1",
			Reward:     200,
			Capability: core.CapabilityValidation,
			InputHash:  "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		},
		{
			TaskID:     "demo_task_data_1",
			Reward:     100,
			Capability: core.CapabilityData,
			InputHash:  "sha256:fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321",
		},
	}
	for _, t := range demotasks {
		if err := mp.PostTask(t); err != nil {
			log.Printf("Warning: seed task %s: %v", t.TaskID, err)
		}
	}
	log.Printf("🗂  Seeded %d demo tasks to marketplace", len(demotasks))
}
