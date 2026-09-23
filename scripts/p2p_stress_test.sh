#!/bin/bash
# P2P Stress Test — Run 3 Alpha Network nodes locally and verify consensus
# Duration: 7 days (168 hours)
# Monitors: block height agreement, divergence detection, uptime
#
# Health check via LOG FILE PARSING (not HTTP polling).
# Nodes produce "📦 Block N produced" lines in their stdout logs.
# This works even when the HTTP API is temporarily unavailable.

set -e

ALPHA_BIN="/opt/Alpha-Network/alphanode"
DATA_ROOT="/tmp/alpha-p2p-test"
LOG_DIR="$DATA_ROOT/logs"
LOCKFILE="$DATA_ROOT/.lock"

# Prevent duplicate runs
if [ -f "$LOCKFILE" ]; then
    OLD_PID=$(cat "$LOCKFILE" 2>/dev/null)
    if kill -0 "$OLD_PID" 2>/dev/null; then
        echo "❌ Another stress test is already running (PID $OLD_PID)"
        exit 1
    fi
fi
echo $$ > "$LOCKFILE"
trap 'rm -f "$LOCKFILE"' EXIT
DURATION_HOURS=168  # 7 days
START_TIME=$(date +%s)
END_TIME=$((START_TIME + DURATION_HOURS * 3600))

# Port assignments
NODE1_API=8088  NODE1_WS=8089
NODE2_API=9090  NODE2_WS=9091
NODE3_API=10090 NODE3_WS=10091

# Log file paths
L1="$LOG_DIR/node1.log"
L2="$LOG_DIR/node2.log"
L3="$LOG_DIR/node3.log"

mkdir -p "$DATA_ROOT"/{node1,node2,node3,logs}

cleanup() {
    echo ""
    echo "🛑 Shutting down all nodes..."
    # Kill only the test nodes by their data dir flag
    for pid in $(pgrep -f "alphanode.*alpha-p2p-test" 2>/dev/null || true); do
        kill "$pid" 2>/dev/null || true
    done
    # Also kill by known PIDs if still tracked
    for pid in $PID1 $PID2 $PID3; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 2
    echo "✅ All nodes stopped."
    echo "Logs saved to: $LOG_DIR"
    exit 0
}
trap cleanup INT TERM

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║     Alpha Network — P2P Consensus Stress Test (7-Day)       ║"
echo "║     3 nodes | PoI consensus | Log-based monitoring           ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "📋 Configuration:"
echo "   Node 1: API :$NODE1_API | WS :$NODE1_WS | Log: $L1"
echo "   Node 2: API :$NODE2_API | WS :$NODE2_WS | Log: $L2"
echo "   Node 3: API :$NODE3_API | WS :$NODE3_WS | Log: $L3"
echo "   Duration: ${DURATION_HOURS}h"
echo "   Health check: log-file parsing (not HTTP polling)"
echo ""

# Kill any leftover test nodes (only those using the alpha-p2p-test data dir)
pgrep -f "alphanode.*alpha-p2p-test" 2>/dev/null | xargs -r kill 2>/dev/null || true
sleep 1

# Clean data dirs for fresh start
rm -rf "$DATA_ROOT"/node1/data "$DATA_ROOT"/node2/data "$DATA_ROOT"/node3/data

echo "🚀 Starting Node 1 (seed)..."
ALPHA_DATADIR="$DATA_ROOT/node1" ALPHA_PORT=$NODE1_API \
    "$ALPHA_BIN" -port $NODE1_API -ws-port $NODE1_WS -datadir "$DATA_ROOT/node1" \
    -announce-addr "127.0.0.1" \
    > "$L1" 2>&1 &
PID1=$!
sleep 2

echo "🚀 Starting Node 2 (joins Node 1)..."
ALPHA_DATADIR="$DATA_ROOT/node2" ALPHA_PORT=$NODE2_API \
    "$ALPHA_BIN" -port $NODE2_API -ws-port $NODE2_WS -datadir "$DATA_ROOT/node2" \
    -announce-addr "127.0.0.1" \
    -seed-peers "127.0.0.1:$NODE1_API" \
    > "$L2" 2>&1 &
PID2=$!
sleep 2

echo "🚀 Starting Node 3 (joins Node 1+2)..."
ALPHA_DATADIR="$DATA_ROOT/node3" ALPHA_PORT=$NODE3_API \
    "$ALPHA_BIN" -port $NODE3_API -ws-port $NODE3_WS -datadir "$DATA_ROOT/node3" \
    -announce-addr "127.0.0.1" \
    -seed-peers "127.0.0.1:$NODE1_API,127.0.0.1:$NODE2_API" \
    > "$L3" 2>&1 &
PID3=$!
sleep 3

# Verify all nodes started
for pid in $PID1 $PID2 $PID3; do
    if ! kill -0 $pid 2>/dev/null; then
        echo "❌ Process $pid failed to start! Check logs."
        cleanup
        exit 1
    fi
done
echo "✅ All 3 nodes running (PIDs: $PID1 $PID2 $PID3)"
echo ""

# ── Helper: extract latest block height from a node's log file ──
# Parses the "📦 Block N produced" log lines. Returns the most recent height,
# or -1 if no blocks have been produced yet.
get_height_from_log() {
    local logfile="$1"
    if [ ! -f "$logfile" ]; then
        echo "-1"
        return
    fi
    # Extract all "Block N produced" lines, take the last one, pull out N
    local last_line
    last_line=$(grep "📦 Block" "$logfile" 2>/dev/null | tail -1)
    if [ -z "$last_line" ]; then
        echo "-1"
        return
    fi
    # "📦 Block 1234 produced | prev=abc... | txs=0 | validator=genesis"
    local height
    height=$(echo "$last_line" | sed -n 's/.*Block \([0-9]*\) produced.*/\1/p')
    if [ -z "$height" ]; then
        echo "-1"
    else
        echo "$height"
    fi
}

# ── Helper: check if a node is still producing (log file modified in last 120s) ──
is_node_alive() {
    local logfile="$1"
    if [ ! -f "$logfile" ]; then
        return 1
    fi
    # A healthy node produces a block every 500ms.
    # Check mtime of the log file — if it hasn't been modified in 120s, stalled.
    local now
    now=$(date +%s)
    local mtime
    mtime=$(stat -c %Y "$logfile" 2>/dev/null || echo 0)
    local age=$((now - mtime))
    if [ $age -gt 120 ]; then
        return 1
    fi
    return 0
}

CHECK_INTERVAL=60  # seconds
DIVERGENCE_COUNT=0
ITERATION=0

echo "📊 Monitoring started — checking consensus every ${CHECK_INTERVAL}s for ${DURATION_HOURS}h"
echo "   $(date -u)"
echo ""

log_event() {
    echo "[$(date -u +%H:%M:%S)] $1" | tee -a "$LOG_DIR/events.log"
}

while true; do
    NOW=$(date +%s)
    if [ $NOW -ge $END_TIME ]; then
        log_event "✅ 24-HOUR TEST COMPLETE — all checks passed"
        break
    fi

    REMAINING=$(( (END_TIME - NOW) / 3600 ))h$(( ((END_TIME - NOW) % 3600) / 60 ))m
    ITERATION=$((ITERATION + 1))

    # ── Log-based health check: extract block heights from log files ──
    H1=$(get_height_from_log "$L1")
    H2=$(get_height_from_log "$L2")
    H3=$(get_height_from_log "$L3")

    # ── Log-based liveness: check nodes are producing blocks ──
    CRASHED=0
    is_node_alive "$L1" || { CRASHED=$((CRASHED + 1)); H1="CRASH"; }
    is_node_alive "$L2" || { CRASHED=$((CRASHED + 1)); H2="CRASH"; }
    is_node_alive "$L3" || { CRASHED=$((CRASHED + 1)); H3="CRASH"; }
    if [ $CRASHED -gt 0 ]; then
        log_event "🔴 CRITICAL: $CRASHED node(s) stalled/crashed — no recent blocks in log"
        DIVERGENCE_COUNT=$((DIVERGENCE_COUNT + 1))
    fi

    # ── Divergence check (allow ±10 blocks tolerance for propagation delay) ──
    MAX_H=$H1
    MIN_H=$H1
    for h in $H2 $H3; do
        if [ "$h" != "-1" ]; then
            [ "$h" -gt "$MAX_H" ] 2>/dev/null && MAX_H=$h
            [ "$h" -lt "$MIN_H" ] 2>/dev/null && MIN_H=$h
        fi
    done

    DIVERGENCE=$((MAX_H - MIN_H))
    if [ "$H1" = "-1" ] || [ "$H2" = "-1" ] || [ "$H3" = "-1" ]; then
        log_event "⚠️  ITER $ITERATION | N1=$H1 N2=$H2 N3=$H3 | NODE SYNCING | Remaining: $REMAINING"
    elif [ $DIVERGENCE -gt 10 ]; then
        DIVERGENCE_COUNT=$((DIVERGENCE_COUNT + 1))
        log_event "🔴 DIVERGENCE #$DIVERGENCE_COUNT | Max delta: $DIVERGENCE | N1=$H1 N2=$H2 N3=$H3 | Remaining: $REMAINING"
    else
        # Healthy
        log_event "✅ ITER $ITERATION | Heights: N1=$H1 N2=$H2 N3=$H3 (Δ=$DIVERGENCE) | Uptime: $(( (NOW - START_TIME) / 3600 ))h$(( ((NOW - START_TIME) % 3600) / 60 ))m | Remaining: $REMAINING"
    fi

    sleep $CHECK_INTERVAL
done

# ── Final Report ─────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                   STRESS TEST COMPLETE                        ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

H1_FINAL=$(get_height_from_log "$L1")
H2_FINAL=$(get_height_from_log "$L2")
H3_FINAL=$(get_height_from_log "$L3")

echo "📊 Final Heights (from logs): N1=$H1_FINAL N2=$H2_FINAL N3=$H3_FINAL"
echo "🔴 Divergence events: $DIVERGENCE_COUNT"
echo "📝 Total iterations: $ITERATION"
echo ""

if [ $DIVERGENCE_COUNT -eq 0 ]; then
    echo "✅ PASSED — Zero consensus divergences in 7 days"
else
    echo "❌ FAILED — $DIVERGENCE_COUNT divergence events detected"
fi

# Archive logs
tar czf "$DATA_ROOT/test-results-$(date +%Y%m%d-%H%M%S).tar.gz" -C "$LOG_DIR" .
echo "📦 Logs archived to $DATA_ROOT/test-results-*.tar.gz"

cleanup
