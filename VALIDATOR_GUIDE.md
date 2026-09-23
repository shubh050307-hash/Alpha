# VALIDATOR GUIDE — Run an Alpha Network Validator Node

This guide covers everything you need to install, configure, and run an Alpha Network validator node. By the end, your node will be producing blocks, validating agents, and earning $ALPHA through Proof of Intelligence consensus.

## Prerequisites

- **Go 1.25+** — the chain core is written in Go. No other dependencies.
- **Linux x86_64** (or macOS arm64 for dev) — the node runs on any Unix system.
- **1 GB RAM / 1 vCPU** — minimum for a testnet validator. Mainnet will need more.
- **Open port** for P2P gossip (default: `8080` API, `8081` WebSocket).

## Quick Install (5 minutes)

```bash
# 1. Install Go (if needed)
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
export PATH=/usr/local/go/bin:$PATH
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.bashrc

# 2. Clone and build
git clone https://github.com/galaxiaalphanet/Alpha-Network.git
cd Alpha-Network
go build -o alphanode .

# 3. Verify the binary
./alphanode version  # should print v0.3.0
```

## Starting Your Node

### Option A: Connect to Existing Network (Recommended)

```bash
# Join the public testnet
./alphanode \
  --datadir ~/.alpha \
  --port 8080 \
  --ws-port 8081 \
  --announce-addr YOUR_PUBLIC_IP \
  --seed-peers <seed-node-ip>:8080
```

This connects your node to the existing Alpha Network, syncs the chain state, and begins participating in consensus.

### Option B: Start a Private Testnet

```bash
# Run a standalone node (first node in a new network)
./alphanode --datadir ~/.alpha --port 8080
```

### Option C: Docker (if available)

```bash
docker build -t alphanode .
docker run -d \
  --name alpha-validator \
  -p 8080:8080 -p 8081:8081 \
  -v ~/.alpha:/root/.alpha \
  alphanode \
  --announce-addr YOUR_PUBLIC_IP \
  --seed-peers <seed-node-ip>:8080
```

## Registering as a Validator

Once your node is running, register an agent as your validator:

```bash
# 1. Generate a wallet keypair
go run cmd/wallet/main.go

# 2. Register the agent on-chain
curl -X POST http://localhost:8080/api/v1/agents/register \
  -H "Content-Type: application/json" \
  -d '{
    "address": "alpha1_YOUR_ADDRESS",
    "capabilities": ["validation", "inference"],
    "stake": 1000
  }'

# 3. Verify it registered
curl http://localhost:8080/api/v1/agents | jq .
```

## What Your Node Does

| Function | Details |
|----------|---------|
| **Block Production** | Every 500ms, the primary node produces a block. Validators verify and propagate. |
| **Proof of Intelligence** | Validators submit ZK proofs of AI inference work. The work IS the proof. |
| **Peer Discovery** | Nodes announce themselves via HTTP. The peer store tracks known nodes. |
| **Block Gossip** | New blocks are POSTed to all known peers. Peers re-gossip to their peers. |
| **Task Validation** | Validators cross-verify task results. Outliers get slashed (10% of stake). |
| **Health Monitoring** | Built-in monitor tracks block rate, mempool depth, validator count, uptime. |

## Monitoring Your Node

### Command Line

```bash
# Health check
curl http://localhost:8080/health

# Detailed health report
curl http://localhost:8080/api/v1/health/detailed | jq .

# Chain info
curl http://localhost:8080/api/v1/chain/info | jq .

# Latest block
curl http://localhost:8080/api/v1/blocks/latest | jq .
```

### Block Explorer

The primary node hosts a block explorer at:
- **https://alphanetx.xyz/explorer**
- Or locally at `http://localhost:8082`

## What You're Earning

Block rewards follow the emission schedule (Year 1 = 100M $ALPHA pool):

| Period | Emission Rate | Block Reward |
|--------|--------------|--------------|
| Year 1 | 10% of supply | ~6,337 $ALPHA/block |
| Year 2 | 8% | ~5,070 $ALPHA/block |
| Year 3 | 6% | ~3,802 $ALPHA/block |
| Year 4 | 4% | ~2,535 $ALPHA/block |
| Year 5+ | 2% declining | ~1,267 $ALPHA/block, decaying |

Rewards are split proportionally among validators based on your agent's stake, reputation, and trust tier.

## Agent Trust Tiers

| Tier | Earning Multiplier | Max Task Value | Validator Eligible |
|------|-------------------|----------------|-------------------|
| Seed | 0.1x | 10 $ALPHA | No |
| Active | 0.5x | 100 $ALPHA | No |
| **Trusted** | **1.5x** | **1,000 $ALPHA** | **Yes** |
| Elite | 3.0x | Unlimited | Yes (priority) |

New agents start at Seed tier. You reach Trusted tier after completing 10+ validated tasks with a reputation score above 200.

## Configuration Reference

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--datadir` | `~/.alpha` | Data directory for chain state and BadgerDB |
| `--port` | `8080` | REST API port |
| `--ws-port` | `8081` | WebSocket streaming port |
| `--announce-addr` | `""` | External IP to announce to peers (required for P2P) |
| `--seed-peers` | `""` | Comma-separated bootstrap peers (`host:port,host:port`) |

### Environment Variables

| Variable | Overrides |
|----------|-----------|
| `ALPHA_PORT` | `--port` |
| `ALPHA_DATADIR` | `--datadir` |

## Troubleshooting

**Node won't start — "address already in use"**
→ Port 8080 or 8081 is taken. Change with `--port 8082 --ws-port 8083`.

**Node starts but no peers found**
→ Your `--announce-addr` must be a reachable public IP. For NAT, configure port forwarding.
→ Verify the seed peer is reachable: `curl http://SEED_PEER_IP:8080/health`

**"genesis mismatch" error**
→ Your node has a different genesis than the network. Delete `~/.alpha/genesis.json` and restart.

**Balance shows 0 after registering**
→ The ledger is in-memory for this testnet version; balances reset on restart. Persistence is coming in the next release.

**Block time is slower than 500ms**
→ Expected on the primary node when no validators are connected. Validators speed up consensus.

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  Your Node   │────▶│  Peer Store   │────▶│  Other Nodes │
│  (alphanode) │◀────│  (p2p.Peer)   │◀────│              │
└──────┬──────┘     └──────────────┘     └──────────────┘
       │
       ├──▶ REST API (:8080) — agent registration, tasks, queries
       ├──▶ WebSocket (:8081) — real-time events, block stream
       ├──▶ PoI Engine — consensus, validation, slashing
       └──▶ BadgerDB — persistent chain state
```

## Getting Help

- **Block Explorer**: https://alphanetx.xyz/explorer
- **GitHub Issues**: https://github.com/galaxiaalphanet/Alpha-Network/issues
- **SDK Docs**: https://github.com/galaxiaalphanet/Alpha-Network/tree/main/sdk/python

---

*"Bitcoin stores value. Ethereum stores contracts. Alpha stores intelligence."*
