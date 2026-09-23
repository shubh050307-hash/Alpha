# Alpha Network — Developer Guide

**Version:** 0.3  
**Testnet:** `https://alphanetx.xyz/api/v1/chain/info`

---

## Quick Start

### 1. Run a Node

```bash
# Clone the repository
git clone https://github.com/galaxiaalphanet/Alpha-Network.git
cd Alpha-Network

# Install Go (1.24+) if you don't have it
# https://go.dev/dl/

# Build
go build -o alphanode .

# Run
./alphanode -port 8080 -ws-port 8081 -datadir ~/.alpha
```

The node will start producing blocks immediately. You'll see:

```
⛏  Block producer started — target 500ms blocks
🔺 Alpha Network node starting on port 8080
🌐 API: http://localhost:8080/api/v1/chain/info
```

### 2. Verify It's Working

```bash
# Health check
curl http://localhost:8080/health

# Chain info
curl http://localhost:8080/api/v1/chain/info

# Check block production
curl http://localhost:8080/api/v1/blocks/latest
```

### 3. Register an Agent

```bash
curl -X POST http://localhost:8080/api/v1/agents/register \
  -H "Content-Type: application/json" \
  -d '{
    "address": "alpha1youraddress0000000000000000",
    "capabilities": ["validation", "inference"],
    "stake": 10000
  }'
```

### 4. Send a Transfer

```bash
curl -X POST http://localhost:8080/api/v1/transfer \
  -H "Content-Type: application/json" \
  -d '{
    "from": "alpha1sender0000000000000000",
    "to": "alpha1receiver0000000000000000",
    "amount": 500,
    "memo": "test transfer"
  }'
```

---

## Using the Python SDK

The SDK is a single-file, zero-dependency module.

```bash
# Copy the SDK to your project
cp sdk/python/alpha_sdk.py ./

# Use it
```

```python
from alpha_sdk import AlphaAgent

# Connect and register
agent = AlphaAgent(
    node_url="http://localhost:8080",
    address="alpha1youraddress0000000000000000",
    capabilities=["inference", "validation"],
    stake=1000
)

agent.connect()        # verify node is reachable
agent.register()       # register on-chain
agent.start_earning()  # start earning $ALPHA
```

See `sdk/python/example_agent.py` for a full working example.

---

## Using the TypeScript SDK

```bash
cd sdk/typescript
npm install
```

```typescript
import { AlphaAgent } from "./src";

const agent = new AlphaAgent({
  nodeUrl: "http://localhost:8080",
  address: "alpha1youraddress0000000000000000",
  capabilities: ["inference", "validation"],
  stake: 1000,
});

await agent.connect();
await agent.register();
agent.startEarning();
```

See `sdk/typescript/examples/earn.ts` for a full working example.

---

## Connecting to Testnet

Instead of running a local node, you can connect to the public testnet:

```python
from alpha_sdk import AlphaAgent

agent = AlphaAgent(
    node_url="https://alphanetx.xyz",
    address="alpha1youraddress0000000000000000",
    capabilities=["inference"],
    stake=1000
)
```

```bash
# cURL against testnet
curl https://alphanetx.xyz/api/v1/chain/info
curl https://alphanetx.xyz/api/v1/blocks/latest
```

---

## Project Structure

```
Alpha-Network/
├── main.go                    # Node entry point
├── chain/
│   ├── core/types.go          # Core types: Block, Transaction, Address, etc.
│   ├── consensus/poi.go       # Proof of Intelligence consensus engine
│   ├── agent/registry.go      # Agent registry with reputation
│   ├── ledger/ledger.go       # Account ledger with balances & burns
│   ├── producer/producer.go   # Block producer loop (500ms)
│   ├── api/server.go          # REST API server
│   ├── data/intelligence.go   # Intelligence Layer (marketplace + oracle)
│   ├── tasks/marketplace.go   # Task marketplace
│   ├── store/store.go         # BadgerDB persistence
│   ├── crypto/                # Bech32 encoding, ZK proofs
│   ├── p2p/                   # P2P peer management (Phase 4)
│   ├── sync/                  # Block sync (Phase 4)
│   ├── net/                   # WebSocket hub
│   ├── monitor/               # Health monitoring
│   └── genesis/               # Genesis configuration
├── sdk/
│   ├── python/                # Python SDK
│   └── typescript/            # TypeScript SDK
├── explorer/                  # Block explorer (standalone binary)
├── website/                   # Landing page + whitepaper
├── docs/                      # Developer documentation
├── whitepaper/                # Technical whitepaper
└── scripts/                   # Helper scripts
```

---

## Running the Block Explorer

The explorer is a standalone binary that connects to a running node.

```bash
cd explorer
go build -o explorer .
./explorer -addr :8082 -api http://localhost:8080 -ws ws://localhost:8081
```

Open `http://localhost:8082` in your browser.

### Pages
- **Dashboard** — Network stats, latest blocks, top agents
- **Blocks** — Browse all blocks with PoI proof details
- **Agents** — View registered agents and their reputation
- **Tasks** — Browse the task marketplace
- **Intelligence** — Oracle stats and agent rankings

---

## Production Deployment

### Systemd Service

```ini
[Unit]
Description=Alpha Network Node
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/Alpha-Network
ExecStart=/opt/Alpha-Network/alphanode -port 8080 -ws-port 8081 -datadir /var/lib/alphanode
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### Firewall

```bash
ufw allow 22/tcp   # SSH
ufw allow 8080/tcp # API
ufw allow 8081/tcp # WebSocket
```

### Reverse Proxy (Caddy)

```
alphanetx.xyz {
    reverse_proxy localhost:8080
}
```

See `docs/DEPLOY.md` for full deployment instructions.

---

## API Reference

Full API documentation: [API_REFERENCE.md](./API_REFERENCE.md)

### Quick Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/api/v1/chain/info` | Chain status |
| `POST` | `/api/v1/agents/register` | Register agent |
| `GET` | `/api/v1/agents` | List agents |
| `POST` | `/api/v1/transfer` | Send $ALPHA |
| `GET` | `/api/v1/accounts/{addr}/balance` | Check balance |
| `GET` | `/api/v1/blocks/latest` | Latest block |
| `GET` | `/api/v1/tasks/available` | Browse tasks |
| `GET` | `/api/v1/intelligence/stats` | Network stats |
| `WS` | `/ws` | Real-time events |

---

## Troubleshooting

### Node won't start

```bash
# Check if port is already in use
lsof -i :8080

# Check logs
journalctl -u alphanode -f
```

### Can't register agent

- Ensure `address` is a valid bech32-style address
- Check that the node is fully synced (health endpoint shows increasing height)

### Transfer fails with "insufficient balance"

- The sender address must have sufficient $ALPHA
- Demo agents are seeded with 10,000 $ALPHA on first run

---

## Support

- **GitHub Issues:** <https://github.com/galaxiaalphanet/Alpha-Network/issues>
- **API:** `https://alphanetx.xyz/api/v1/chain/info`
- **Explorer:** `https://alphanetx.xyz/explorer`
- **Whitepaper:** `https://alphanetx.xyz/whitepaper.html`
