#!/usr/bin/env bash
# Alpha Network — integration test suite
# Starts the node, runs API smoke tests, reports PASS/FAIL for each step.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHAIN_BIN="$PROJECT_ROOT/alphanode"
API="http://localhost:8089"        # use a non-default port to avoid conflicts
DATA_DIR="/tmp/alpha_test_$$"
NODE_PID=""

PASS=0
FAIL=0

# ── Colors ────────────────────────────────────────────────────────────────────
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "  ${GREEN}✅ PASS${NC} — $1"; PASS=$((PASS+1)); }
fail() { echo -e "  ${RED}❌ FAIL${NC} — $1"; FAIL=$((FAIL+1)); }
info() { echo -e "  ${YELLOW}ℹ  ${NC} $1"; }

# ── Cleanup ───────────────────────────────────────────────────────────────────
cleanup() {
    [ -n "$NODE_PID" ] && kill "$NODE_PID" 2>/dev/null || true
    rm -rf "$DATA_DIR"
}
trap cleanup EXIT

# ── Build ─────────────────────────────────────────────────────────────────────
echo ""
echo "⚡ Alpha Network — Integration Test"
echo "════════════════════════════════════"
echo ""
info "Building chain binary..."
cd "$PROJECT_ROOT"
if go build -o "$CHAIN_BIN" . 2>&1; then
    pass "go build succeeded"
else
    fail "go build failed"
    exit 1
fi

# ── Start node ────────────────────────────────────────────────────────────────
mkdir -p "$DATA_DIR/data"
info "Starting node on port 8089..."
"$CHAIN_BIN" --datadir "$DATA_DIR" --port 8089 --ws-port 8088 >/tmp/alpha_test_node.log 2>&1 &
NODE_PID=$!

# Wait for health
info "Waiting for node health..."
HEALTHY=0
for i in $(seq 1 30); do
    if curl -sf "$API/health" >/dev/null 2>&1; then
        HEALTHY=1
        break
    fi
    sleep 1
done

if [ "$HEALTHY" -eq 1 ]; then
    pass "node started and health endpoint responded"
else
    fail "node did not become healthy within 30s"
    cat /tmp/alpha_test_node.log
    exit 1
fi

# ── Test: /health ─────────────────────────────────────────────────────────────
echo ""
echo "── API Smoke Tests ──────────────────────────────────────────────"

STATUS=$(curl -sf "$API/health" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status',''))" 2>/dev/null || echo "")
if [ "$STATUS" = "ok" ]; then
    pass "/health returns status=ok"
else
    fail "/health — unexpected status: '$STATUS'"
fi

# ── Test: /api/v1/chain/info ──────────────────────────────────────────────────
HEIGHT=$(curl -sf "$API/api/v1/chain/info" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('chain_id',''))" 2>/dev/null || echo "")
if [ "$HEIGHT" = "alpha-1" ]; then
    pass "/api/v1/chain/info returns chain_id=alpha-1"
else
    fail "/api/v1/chain/info — chain_id mismatch: '$HEIGHT'"
fi

# ── Test: Register an agent ───────────────────────────────────────────────────
REGISTER_RESP=$(curl -sf -X POST "$API/api/v1/agents/register" \
    -H "Content-Type: application/json" \
    -d '{"address":"alpha1test000000000000000000000000","capabilities":["validation","inference"],"stake":5000}' \
    2>/dev/null || echo "{}")

AGENT_ID=$(echo "$REGISTER_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('agent_id',''))" 2>/dev/null || echo "")
if [ -n "$AGENT_ID" ]; then
    pass "registered test agent: $AGENT_ID"
else
    fail "agent registration failed — response: $REGISTER_RESP"
fi

# ── Test: Check balance ───────────────────────────────────────────────────────
if [ -n "$AGENT_ID" ]; then
    AGENT_ADDR="alpha_agent_$AGENT_ID"
    BAL_RESP=$(curl -sf "$API/api/v1/accounts/$AGENT_ADDR/balance" 2>/dev/null || echo "{}")
    BAL=$(echo "$BAL_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('balance','-1'))" 2>/dev/null || echo "-1")
    if [ "$BAL" -ge "0" ] 2>/dev/null; then
        pass "account balance returned: $BAL \$ALPHA"
    else
        fail "account balance endpoint failed — response: $BAL_RESP"
    fi
fi

# ── Test: Post a task ─────────────────────────────────────────────────────────
TASK_RESP=$(curl -sf -X POST "$API/api/v1/tasks/post" \
    -H "Content-Type: application/json" \
    -d '{"capability":"inference","reward":250,"input_hash":"sha256:test000000000000","posted_by":"alpha1test000000000000000000000000"}' \
    2>/dev/null || echo "{}")
TASK_ID=$(echo "$TASK_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('task_id',''))" 2>/dev/null || echo "")
if [ -n "$TASK_ID" ]; then
    pass "posted task: $TASK_ID"
else
    fail "task posting failed — response: $TASK_RESP"
fi

# ── Test: Get available tasks ─────────────────────────────────────────────────
TASKS_RESP=$(curl -sf "$API/api/v1/tasks/available?capability=inference" 2>/dev/null || echo "{}")
TASK_COUNT=$(echo "$TASKS_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('count',0))" 2>/dev/null || echo "0")
if [ "$TASK_COUNT" -gt "0" ] 2>/dev/null; then
    pass "available tasks returned: $TASK_COUNT task(s)"
else
    fail "no available tasks found — response: $TASKS_RESP"
fi

# ── Test: Intelligence stats ──────────────────────────────────────────────────
INTEL_RESP=$(curl -sf "$API/api/v1/intelligence/stats" 2>/dev/null || echo "{}")
INTEL_OK=$(echo "$INTEL_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('success',''))" 2>/dev/null || echo "")
if [ "$INTEL_OK" = "True" ] || [ "$INTEL_OK" = "true" ]; then
    pass "intelligence stats endpoint responded"
else
    fail "intelligence stats failed — response: $INTEL_RESP"
fi

# ── Test: Block production ────────────────────────────────────────────────────
info "Waiting 3s for block production..."
sleep 3
HEIGHT=$(curl -sf "$API/api/v1/chain/info" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('height',0))" 2>/dev/null || echo "0")
if [ "$HEIGHT" -gt "0" ] 2>/dev/null; then
    pass "blocks produced — chain height: $HEIGHT"
else
    fail "no blocks produced after 3s"
fi

# ── Test: Latest block endpoint ───────────────────────────────────────────────
LATEST_RESP=$(curl -sf "$API/api/v1/blocks/latest" 2>/dev/null || echo "{}")
LATEST_OK=$(echo "$LATEST_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('success',''))" 2>/dev/null || echo "")
if [ "$LATEST_OK" = "True" ] || [ "$LATEST_OK" = "true" ]; then
    pass "/api/v1/blocks/latest returned a block"
else
    fail "/api/v1/blocks/latest failed — response: $LATEST_RESP"
fi

# ── Test: Health detailed endpoint ────────────────────────────────────────────
HEALTH_D=$(curl -sf "$API/api/v1/health/detailed" 2>/dev/null || echo "{}")
HD_STATUS=$(echo "$HEALTH_D" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status',''))" 2>/dev/null || echo "")
if [ -n "$HD_STATUS" ]; then
    pass "/api/v1/health/detailed returned status: $HD_STATUS"
else
    fail "/api/v1/health/detailed failed — response: $HEALTH_D"
fi

# ── Results ───────────────────────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════"
TOTAL=$((PASS+FAIL))
echo -e "  Results: ${GREEN}$PASS/$TOTAL passed${NC}  ${RED}$FAIL failed${NC}"
echo "════════════════════════════════════"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo "❌ Integration tests FAILED ($FAIL failure(s))"
    exit 1
else
    echo "✅ All integration tests PASSED"
    exit 0
fi
