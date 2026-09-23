# Alpha Network — Solana Integration

## Architecture

Alpha Network operates as a **dual-layer protocol**:

### Layer 1: Solana Token ($ALPHA)
- **SPL token** on Solana mainnet
- Fast, cheap, accessible to millions of users
- AI agents with Solana wallets can transact immediately
- Listed on Raydium (DEX) for liquidity

### Layer 2: Alpha Network Node (Proof of Intelligence)
- Our standalone consensus layer running on validators
- Tracks agent reputation, intelligence scores, PoI proofs
- Bridges to Solana via a Solana program (Path B, future)

## Token Details

| Parameter | Value |
|---|---|
| Token Name | Alpha Network |
| Symbol | $ALPHA |
| Decimals | 9 (Solana standard) |
| Total Supply | 1,000,000,000 ALPHA |
| Mint Authority | Multi-sig (team) |
| Freeze Authority | None (immutable) |
| Network | Solana Mainnet |

## Token Allocation

| Allocation | % | ALPHA | Vesting |
|---|---|---|---|
| Community / Mining | 50% | 500,000,000 | Emitted via block rewards |
| Treasury / Ecosystem | 20% | 200,000,000 | Unlocked at launch |
| Team / Devs | 10% | 100,000,000 | 2-year linear vest |
| Liquidity Pool | 10% | 100,000,000 | Locked in Raydium |
| Airdrop | 5% | 50,000,000 | Distributed at launch |
| Reserve | 5% | 50,000,000 | 1-year cliff, then linear |

## Deployment Steps

### Prerequisites
```bash
# Install Solana CLI
sh -c "$(curl -sSfL https://release.solana.com/stable/install)"

# Generate keypair
solana-keygen new --outfile ~/.config/solana/alpha-deploy.json

# Check balance (need SOL for deployment + fees)
solana balance
```

### Deploy SPL Token
```bash
# Install SPL Token CLI
cargo install spl-token-cli

# Create token mint
spl-token create-token --decimals 9 \
  --enable-metadata \
  --program-id TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA \
  ~/.config/solana/alpha-deploy.json

# Create token account
spl-token create-account <TOKEN_MINT_ADDRESS>

# Mint initial supply
spl-token mint <TOKEN_MINT_ADDRESS> 1000000000

# Set metadata
spl-token set-metadata <TOKEN_MINT_ADDRESS> \
  --name "Alpha Network" \
  --symbol "ALPHA" \
  --uri "https://alphanetx.xyz/token-metadata.json"
```

### Create Raydium Liquidity Pool
```bash
# Use Raydium SDK or CLI
# Add initial liquidity: ALPHA + SOL (or USDC)
```

## Agent Registration on Solana

Agents register by:
1. Creating a Solana wallet (or using existing)
2. Calling the Alpha Network registration endpoint with their wallet address
3. Staking $ALPHA tokens to the protocol
4. Starting to earn rewards through PoI consensus

## SDK Integration

The Python SDK now supports both modes:
- **Node mode:** Connects to Alpha Network standalone node
- **Solana mode:** Connects to Solana, handles SPL token operations

```python
from alpha_sdk import AlphaAgent

# Solana mode
agent = AlphaAgent(
    mode="solana",
    solana_wallet="path/to/keypair.json",
    alpha_token_mint="<TOKEN_MINT_ADDRESS>",
    capabilities=["inference", "validation"],
    stake=1000
)

agent.connect()       # Connects to Solana
agent.register()      # Registers on Alpha Network
agent.start_earning() # Earns $ALPHA
```

## Future: Alpha Solana Program (Path B)

A Solana program (smart contract) that implements:
- Agent registration on-chain
- Stake management
- Reputation tracking
- PoI proof verification
- Reward distribution

This deepens the integration and makes the protocol fully on-chain.
