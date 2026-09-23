# Alpha Network — Mainnet Launch Checklist

> Target: Alpha Mainnet (`alpha-mainnet-1`) | Genesis: `genesis_mainnet.json` | Status: Pre-launch

---

## Pre-Flight (Security & Audit)

- [x] **1.1** Fix remaining MEDIUM audit finding: `RunConsensus` proof depletion on quorum failure (`consensus/poi.go`)
- [x] **1.2** Fix burn semantics inconsistency: `BurnSupply` vs `BurnFromProtocol` (`ledger/ledger.go`)
- [x] **1.3** Add P2P block deduplication cache — prevent gossip loops (`p2p/node.go`)
- [x] **1.4** Add per-address nonce tracking for transfer replay protection (`ledger/ledger.go`)
- [x] **1.5** Restrict CORS to specific origins for mainnet (`api/server.go`)
- [x] **1.6** Sanitize error messages — remove internal state leakage from API responses
- [ ] **1.7** External security audit — engage a third-party auditor for consensus + ledger
- [ ] **1.8** Penetration test — fuzz all API endpoints, test rate limiter under load

## Infrastructure

- [ ] **2.1** Deploy 3+ geographically distributed seed nodes (not all on one VPS)
- [ ] **2.2** Configure DNS: `rpc.alphanetx.xyz` → seed nodes, `explorer.alphanetx.xyz` → explorer
- [ ] **2.3** SSL/TLS via Caddy — obtain Let's Encrypt certs for all public endpoints
- [ ] **2.4** Set up monitoring: Prometheus + Grafana dashboards for all nodes
- [ ] **2.5** Set up alerting: PagerDuty or Grafana alerts for node downtime, chain halt, divergence
- [ ] **2.6** Configure firewall: allow only ports 8080 (API), 8082 (explorer) on public IPs
- [ ] **2.7** Back up genesis.json and seed node data dirs to encrypted offsite storage
- [ ] **2.8** Document node operator runbook — how to join, sync, monitor, troubleshoot

## Tokenomics & Genesis

- [ ] **3.1** Finalize `genesis_mainnet.json` — review all parameters with economic analysis
- [ ] **3.2** Confirm 0 founder allocation, 0 VC, 0 pre-mine — protocol treasuries only
- [ ] **3.3** Set genesis time at least 2 weeks out — announce to validators
- [ ] **3.4** Onboard initial validator set — coordinate key generation + announce addresses
- [ ] **3.5** Populate `genesis_validators` in `genesis_mainnet.json` with initial validator pubkeys
- [ ] **3.6** Generate genesis block hash — publish and pin (IPFS + Twitter + website)
- [ ] **3.7** Verify total supply = 1,000,000,000 $ALPHA with auditor tooling

## Consensus & Network

- [ ] **4.1** Run 7-day multi-node testnet with 5+ validators — zero divergence required
- [ ] **4.2** Test network partition recovery — nodes re-sync after isolation
- [ ] **4.3** Test validator rotation — add/remove validators without chain halt
- [ ] **4.4** Test slashing — verify dead agents are correctly slashed and tasks reassigned
- [ ] **4.5** Verify PoI consensus with 2/3+1 quorum under realistic validator count
- [ ] **4.6** Test ZK proof verification at scale — 1000+ proofs per block

## SDK & Developer Tooling

- [ ] **5.1** Python SDK v1.0 published to PyPI with Ed25519 signing
- [x] **5.2** TypeScript SDK v1.0 published to npm with Ed25519 signing — ✅ Live at https://www.npmjs.com/package/alpha-network-sdk
- [ ] **5.3** SDK documentation site — quickstart, API reference, examples
- [ ] **5.4** OpenClaw integration skill finalized and tested
- [ ] **5.5** Hermes integration skill finalized and tested
- [ ] **5.6** Block explorer polished — search, mobile responsive, status badges
- [x] **5.7** Faucet for testnet $ALPHA (rate-limited, anti-bot) — built at `cmd/faucet/`

## Token Launch

- [ ] **6.1** Solana SPL token program audited and deployed (Anchor)
- [ ] **6.2** Bridge contract between Alpha L1 and Solana SPL tested
- [ ] **6.3** Token metadata: name, symbol, icon submitted to Solana token list
- [ ] **6.4** DEX liquidity plan — initial pool, LP tokens locked/timelocked
- [ ] **6.5** Token distribution plan published — no insiders, no team allocation
- [ ] **6.6** Exchange listing outreach (post-genesis, not pre)

## Communications

- [ ] **7.1** Website updated: mainnet countdown, validator stats, real-time chain data
- [ ] **7.2** Whitepaper v1.0 finalized — include tokenomics, consensus spec, threat model
- [ ] **7.3** Announcement thread drafted — Twitter, Discord, Reddit, Hacker News
- [ ] **7.4** Validator onboarding guide published (`VALIDATOR_GUIDE.md`)
- [ ] **7.5** Community channels set up: Discord, Telegram (read-only announcements)
- [ ] **7.6** Press kit: logos, brand assets, one-pager

## Launch Day

- [ ] **8.1** Final binary tagged and hash-published (GitHub release + IPFS)
- [ ] **8.2** Genesis ceremony: all validators confirm genesis.json hash matches
- [ ] **8.3** Start seed nodes → validators connect → first block produced
- [ ] **8.4** Verify block explorer shows genesis block and first blocks
- [ ] **8.5** Verify SDKs can connect, register agents, submit proofs
- [ ] **8.6** Monitor chain for 24h — no halts, no divergences
- [ ] **8.7** Announce mainnet live 🚀

---

> **Status:** 9/52 items complete. Pre-launch phase.
> **Next milestone:** npm login + publish (5.2), external audit (1.7–1.8), seed nodes (2.x).
