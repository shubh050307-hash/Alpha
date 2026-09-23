#!/usr/bin/env bash
# ⚡ Alpha Network — Deploy $ALPHA SPL Token to Solana Mainnet
# =============================================================
# DO NOT RUN THIS SCRIPT YET. Read carefully. Execute after presale closes.
#
# Prerequisites:
#   - Solana CLI installed:   sh -c "$(curl -sSfL https://release.solana.com/stable/install)"
#   - Solana mainnet RPC:     solana config set --url https://api.mainnet-beta.solana.com
#   - A funded wallet:        ~0.5 SOL needed for deployment + rent
#   - Anchor installed:       cargo install --git https://github.com/coral-xyz/anchor anchor-cli
#
# Steps this script performs:
#   1. Create a new SPL token:          spl-token create-token
#   2. Create token metadata:           metaboss (optional, for explorer display)
#   3. Create associated token account
#   4. Mint initial supply
#   5. Deploy Anchor program (if using custom program)
#   6. Verify deployment
#
# ⚠️  WARNING: This spends real SOL on mainnet. There is no undo.
# ⚠️  Run --dry-run first. Read every prompt before continuing.

set -e

# ── Configuration ─────────────────────────────────────────────────────────────
TOKEN_NAME="Alpha Network"
TOKEN_SYMBOL="ALPHA"
TOKEN_DECIMALS=8
TOTAL_SUPPLY=1000000000  # 1 billion (in base units = 1e8 per token)
MINT_AUTHORITY=""        # Set to your wallet pubkey. Leave empty to disable future minting.
DEPLOY_WALLET="/root/.config/solana/alpha-deploy.json"
METADATA_URI="ipfs://bafkreidcsa3g7ooarwwbaoekm76mfxsmr6onae5uuhwjehj4omeak6iy2q"  # Locked — IPFS metadata URI
TREASURY_WALLET="BypHj4Y4f9J5ajAu28gJZFJCR1cLr3CTU8E86THMK1bi"

NETWORK="mainnet-beta"
RPC_URL="https://solana-rpc.publicnode.com"

# ── Colors ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GRN='\033[0;32m'
YLW='\033[1;33m'
CYN='\033[0;36m'
NC='\033[0m'

echo -e "${CYN}"
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║     Alpha Network — Deploy \$ALPHA to Solana Mainnet          ║"
echo "║     ⚠️  REAL SOL REQUIRED — THERE IS NO UNDO                  ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo ""

# ── Arg parsing ──────────────────────────────────────────────────────────────
DRY_RUN=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --execute) DRY_RUN=false; shift ;;
        --dry-run) DRY_RUN=true; shift ;;
        --wallet)  DEPLOY_WALLET="$2"; shift 2 ;;
        --mint-authority) MINT_AUTHORITY="$2"; shift 2 ;;
        *)
            echo "Usage: $0 [--dry-run|--execute] [--wallet PATH] [--mint-authority PUBKEY]"
            exit 1
            ;;
    esac
done

# ── Pre-flight Checks ─────────────────────────────────────────────────────────
echo "📋 Pre-flight checks..."
echo ""

# Check Solana CLI
if ! command -v solana &>/dev/null; then
    echo -e "${RED}❌ Solana CLI not found.${NC}"
    echo "   Install: sh -c \"\$(curl -sSfL https://release.solana.com/stable/install)\""
    exit 1
fi
echo -e "   ${GRN}✅${NC} Solana CLI: $(solana --version)"

# Check network
CURRENT_NET=$(solana config get | grep "RPC URL" | awk '{print $NF}')
if [[ "$CURRENT_NET" != *"mainnet"* ]] && [ "$DRY_RUN" = false ]; then
    echo ""
    echo -e "${YLW}⚠️  Current Solana config is NOT mainnet!${NC}"
    echo "   Current: $CURRENT_NET"
    echo ""
    read -p "   Set to mainnet? (y/N) " confirm
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        solana config set --url "$RPC_URL"
    else
        echo "   Aborted. Set manually: solana config set --url $RPC_URL"
        exit 1
    fi
fi
echo -e "   ${GRN}✅${NC} Network: $CURRENT_NET"

# Check wallet
WALLET_ADDR=$(solana address 2>/dev/null || echo "")
if [ -z "$WALLET_ADDR" ]; then
    echo -e "${RED}❌ No Solana wallet configured.${NC}"
    echo "   Create one: solana-keygen new"
    exit 1
fi
echo -e "   ${GRN}✅${NC} Wallet: $WALLET_ADDR"

# Check balance
BALANCE=$(solana balance 2>/dev/null | awk '{print $1}' || echo "0")
echo -e "   ${GRN}✅${NC} Balance: $BALANCE SOL"

MIN_SOL_NEEDED=0.5
if (( $(echo "$BALANCE < $MIN_SOL_NEEDED" | bc -l 2>/dev/null || echo 1) )); then
    echo ""
    echo -e "${RED}❌ Insufficient SOL balance.${NC}"
    echo "   Need at least $MIN_SOL_NEEDED SOL for token creation + rent."
    echo "   Current balance: $BALANCE SOL"
    echo "   Fund this wallet and retry."
    exit 1
fi

echo ""
echo "   Token:  $TOKEN_NAME (\$$TOKEN_SYMBOL)"
echo "   Supply: $TOTAL_SUPPLY (${TOKEN_DECIMALS} decimals)"
echo "   Mint:   ${MINT_AUTHORITY:-<disabled — fixed supply>}"
echo ""

if $DRY_RUN; then
    echo -e "${YLW}📋 DRY-RUN MODE — no transactions will be sent${NC}"
    echo ""
    echo "   Would execute:"
    echo "   1. spl-token create-token --decimals $TOKEN_DECIMALS"
    echo "   2. spl-token create-account <TOKEN_MINT>"
    echo "   3. spl-token mint <TOKEN_MINT> $TOTAL_SUPPLY"
    echo "   4. spl-token authorize <TOKEN_MINT> mint --disable  (if no mint authority)"
    echo ""
    echo "   To execute: $0 --execute"
    exit 0
fi

echo -e "${RED}"
echo "   ⚠️  ⚠️  ⚠️   REAL MAINNET DEPLOYMENT   ⚠️  ⚠️  ⚠️"
echo ""
echo "   This will deploy \$ALPHA to Solana MAINNET."
echo "   Token address will be PERMANENT."
echo "   Supply: $TOTAL_SUPPLY $TOKEN_SYMBOL"
echo "   Wallet: $WALLET_ADDR (balance: $BALANCE SOL)"
echo ""
echo -e "${NC}"
read -p "   Type 'DEPLOY \$ALPHA TO MAINNET' to confirm: " confirm
if [ "$confirm" != "DEPLOY \$ALPHA TO MAINNET" ]; then
    echo "   Aborted."
    exit 1
fi

# ── Step 1: Create SPL Token ──────────────────────────────────────────────────
echo ""
echo -e "${CYN}━━━ Step 1: Create SPL Token ━━━${NC}"
TOKEN_MINT=$(spl-token create-token --decimals $TOKEN_DECIMALS 2>&1 | grep "Creating token" | grep -oP '[A-Za-z0-9]{32,44}')
if [ -z "$TOKEN_MINT" ]; then
    # Alternate parse — spl-token outputs differently on some versions
    TOKEN_MINT=$(spl-token create-token --decimals $TOKEN_DECIMALS 2>&1 | tail -1 | awk '{print $NF}')
fi
echo -e "   ${GRN}✅ Token created:${NC} $TOKEN_MINT"
echo ""

# ── Step 2: Create Token Account ──────────────────────────────────────────────
echo -e "${CYN}━━━ Step 2: Create Token Account ━━━${NC}"
TOKEN_ACCOUNT=$(spl-token create-account "$TOKEN_MINT" 2>&1 | grep "Creating account" | grep -oP '[A-Za-z0-9]{32,44}')
if [ -z "$TOKEN_ACCOUNT" ]; then
    TOKEN_ACCOUNT=$(spl-token create-account "$TOKEN_MINT" 2>&1 | tail -1 | awk '{print $NF}')
fi
echo -e "   ${GRN}✅ Token account:${NC} $TOKEN_ACCOUNT"
echo ""

# ── Step 3: Mint Supply ───────────────────────────────────────────────────────
echo -e "${CYN}━━━ Step 3: Mint Supply ━━━${NC}"
spl-token mint "$TOKEN_MINT" "$TOTAL_SUPPLY"
echo -e "   ${GRN}✅ Minted $TOTAL_SUPPLY $TOKEN_SYMBOL${NC}"
echo ""

# ── Step 4: Transfer to Treasury ─────────────────────────────────────────────
echo -e "${CYN}━━━ Step 4: Transfer Full Supply to Treasury ━━━${NC}"
# Create treasury token account if it doesn't exist, then transfer all tokens
spl-token transfer "$TOKEN_MINT" 1000000000 "$TREASURY_WALLET" --fund-recipient
echo -e "   ${GRN}✅ Transferred $TOTAL_SUPPLY $TOKEN_SYMBOL to treasury:${NC} $TREASURY_WALLET"
echo ""

# ── Step 5: Disable Future Minting (fixed supply) ─────────────────────────────
if [ -z "$MINT_AUTHORITY" ]; then
    echo -e "${CYN}━━━ Step 5: Lock Supply (disable mint authority) ━━━${NC}"
    spl-token authorize "$TOKEN_MINT" mint --disable
    echo -e "   ${GRN}✅ Mint authority disabled — supply is permanently fixed${NC}"
else
    echo -e "${CYN}━━━ Step 5: Set Mint Authority ━━━${NC}"
    spl-token authorize "$TOKEN_MINT" mint "$MINT_AUTHORITY"
    echo -e "   ${GRN}✅ Mint authority set to:${NC} $MINT_AUTHORITY"
fi
echo ""

# ── Step 6: Verify ────────────────────────────────────────────────────────────
echo -e "${CYN}━━━ Step 6: Verify Deployment ━━━${NC}"
TOKEN_SUPPLY=$(spl-token supply "$TOKEN_MINT" 2>&1 | awk '{print $NF}')
TOKEN_INFO=$(spl-token account-info "$TOKEN_MINT" 2>&1)
echo "   Token Mint:    $TOKEN_MINT"
echo "   Token Account: $TOKEN_ACCOUNT"
echo "   Total Supply:  $TOKEN_SUPPLY"
echo ""
echo -e "${GRN}✅ \$ALPHA deployed to Solana mainnet${NC}"
echo ""

# ── Step 7: Set Metadata URI ──────────────────────────────────────────────────
echo -e "${CYN}━━━ Step 7: Token Metadata ━━━${NC}"
if [ -n "$METADATA_URI" ]; then
    echo "   Metadata URI: $METADATA_URI"
    echo "   ⚠️  Run metaboss (or Metaplex SDK) to set on-chain metadata:"
    echo "   metaboss create metadata --mint $TOKEN_MINT --uri \"$METADATA_URI\""
else
    echo -e "   ${YLW}⚠️  METADATA_URI not set — metadata will NOT be attached${NC}"
    echo "   Set METADATA_URI at top of script after Pinata upload, then re-run"
fi
echo ""

# ── Summary ───────────────────────────────────────────────────────────────────
echo -e "${CYN}╔══════════════════════════════════════════════════════════════╗${NC}"
echo -e "${CYN}║                   DEPLOYMENT COMPLETE                         ║${NC}"
echo -e "${CYN}╚══════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "   Token Mint:    $TOKEN_MINT"
echo "   Token Account: $TOKEN_ACCOUNT"
echo "   Supply:        $TOTAL_SUPPLY $TOKEN_SYMBOL"
echo "   Decimals:      $TOKEN_DECIMALS"
echo "   Fixed Supply:  ${MINT_AUTHORITY:+No (authority: $MINT_AUTHORITY)}${MINT_AUTHORITY:-Yes (mint disabled)}"
echo ""
echo "   🔗 Solscan:   https://solscan.io/token/$TOKEN_MINT"
echo "   🔗 Explorer:  https://explorer.solana.com/address/$TOKEN_MINT"
echo ""
echo "   Treasury:      $TREASURY_WALLET"
echo "   Metadata URI:  ${METADATA_URI:-<not set — upload to Pinata first>}"
echo ""
echo "   Next steps:"
echo "   1. Verify on Solscan that the token appears correctly"
echo "   2. Set on-chain metadata with metaboss or Metaplex SDK"
echo "   3. Run post-presale-migrate.sh to generate distribution CSV"
echo "   4. Distribute tokens to presale contributors"
echo "   5. Create Raydium liquidity pool with remaining treasury balance"
echo "   6. Update alphanetx.xyz with the token address"
echo ""
echo "   ⚠️  SAVE THE TOKEN MINT ADDRESS. There is no recovery."
echo "   ⚠️  SAVE THE WALLET KEYPAIR. You need it to manage the token."
echo ""
