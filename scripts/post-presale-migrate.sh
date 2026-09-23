#!/usr/bin/env bash
# ⚡ Alpha Network — Post-Presale Migration Script
# =================================================
# Run after presale closes (May 30, 2026 12:00 UTC).
# Exports all contributions to CSV and prints summary.
#
# Usage:
#   ./scripts/post-presale-migrate.sh                  # uses localhost:8080
#   ./scripts/post-presale-migrate.sh --api HOST:PORT   # custom node
#   ./scripts/post-presale-migrate.sh --csv out.csv     # custom output path

set -e

# ── Configuration ─────────────────────────────────────────────────────────────
API_URL="http://localhost:8080"
CSV_FILE="presale_distribution.csv"

# ── Colors ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GRN='\033[0;32m'
YLW='\033[1;33m'
CYN='\033[0;36m'
NC='\033[0m'

# ── Arg parsing ──────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --api) API_URL="$2"; shift 2 ;;
        --csv) CSV_FILE="$2"; shift 2 ;;
        *)
            echo "Usage: $0 [--api HOST:PORT] [--csv OUTPUT.csv]"
            exit 1
            ;;
    esac
done

echo -e "${CYN}"
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║     Alpha Network — Post-Presale Migration                   ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo ""
echo "   API:  $API_URL"
echo "   CSV:  $CSV_FILE"
echo ""

# ── 1. Fetch presale stats ────────────────────────────────────────────────────
echo -e "${CYN}━━━ Fetching presale stats... ━━━${NC}"
STATS_JSON=$(curl -sS --connect-timeout 10 --max-time 30 "$API_URL/api/v1/presale/stats" 2>&1)
if [ $? -ne 0 ]; then
    echo -e "${RED}❌ Failed to reach node at $API_URL${NC}"
    echo "   Error: $STATS_JSON"
    exit 1
fi

# Validate JSON
if ! echo "$STATS_JSON" | python3 -m json.tool >/dev/null 2>&1; then
    echo -e "${RED}❌ Invalid JSON response${NC}"
    echo "   Raw: $STATS_JSON"
    exit 1
fi
echo -e "   ${GRN}✅ Stats fetched${NC}"
echo ""

# ── 2. Extract summary ────────────────────────────────────────────────────────
TOTAL_SOL=$(echo "$STATS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('total_sol',0))")
TOTAL_PARTICIPANTS=$(echo "$STATS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('total_participants',0))")
TOTAL_ALPHA=$(echo "$STATS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('total_alpha_allocated',0))")
HARD_CAP=$(echo "$STATS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('hard_cap_sol',119))")
PCT_FILLED=$(echo "$STATS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('percent_filled',0))")

# ── 3. Generate CSV ───────────────────────────────────────────────────────────
echo -e "${CYN}━━━ Generating $CSV_FILE... ━━━${NC}"
echo "wallet_address,sol_contributed,alpha_allocation" > "$CSV_FILE"

echo "$STATS_JSON" | python3 -c "
import sys, json, csv
data = json.load(sys.stdin)
contributions = data.get('contributions', [])
if not contributions:
    print('WARNING: No contributions found', file=sys.stderr)
    sys.exit(0)
with open('$CSV_FILE', 'a') as f:
    writer = csv.writer(f)
    for c in contributions:
        writer.writerow([
            c.get('wallet', ''),
            c.get('sol_amount', 0),
            c.get('alpha_allocation', 0)
        ])
count = len(contributions)
total_sol = sum(c.get('sol_amount', 0) for c in contributions)
total_alpha = sum(c.get('alpha_allocation', 0) for c in contributions)
print(f'ROWS={count}|SOL={total_sol}|ALPHA={total_alpha}')
"

ROW_COUNT=$(wc -l < "$CSV_FILE")
ROW_COUNT=$((ROW_COUNT - 1))  # subtract header
echo -e "   ${GRN}✅ CSV written: $CSV_FILE${NC}"
echo ""

# ── 4. Print summary ─────────────────────────────────────────────────────────
echo -e "${CYN}╔══════════════════════════════════════════════════════════════╗${NC}"
echo -e "${CYN}║                   PRESALE SUMMARY                            ║${NC}"
echo -e "${CYN}╚══════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "   ${CYN}Total Participants:${NC}  $TOTAL_PARTICIPANTS"
echo -e "   ${CYN}Total SOL Raised:${NC}    $TOTAL_SOL SOL  (of $HARD_CAP SOL hard cap)"
echo -e "   ${CYN}Cap Filled:${NC}         $PCT_FILLED%"
echo -e "   ${CYN}Total \$ALPHA to Distribute:${NC}  $TOTAL_ALPHA"
echo -e "   ${CYN}CSV Rows Written:${NC}   $ROW_COUNT"
echo ""
echo -e "   ${GRN}CSV file: $(pwd)/$CSV_FILE${NC}"
echo ""
echo "   Next steps:"
echo "   1. Review $CSV_FILE — verify wallet addresses and allocations"
echo "   2. Deploy \$ALPHA SPL token to Solana mainnet"
echo "   3. Distribute tokens to wallets in $CSV_FILE"
echo "   4. Create Raydium LP with remaining treasury balance"
echo ""
echo -e "${GRN}✅ Done.${NC}"
