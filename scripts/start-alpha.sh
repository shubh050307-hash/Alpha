#!/bin/bash
# Alpha Network — Start All Services
# This script starts alphanode + explorer and tracks PIDs.
# Designed to run on boot (cron @reboot) or manually.

set -euo pipefail

DATA_DIR="${ALPHA_DATA_DIR:-/root/.alpha}"
NODE_BIN="/opt/Alpha-Network/alphanode"
EXPLORER_BIN="/opt/Alpha-Network/explorer/explorer"
NODE_PORT="${ALPHA_NODE_PORT:-8080}"
WS_PORT="${ALPHA_WS_PORT:-8081}"
EXPLORER_PORT="${ALPHA_EXPLORER_PORT:-8082}"
PID_FILE="/var/run/alpha-network.pid"
LOG_DIR="/var/log"

mkdir -p "$DATA_DIR" "$LOG_DIR"

# Kill any existing processes
for pid in $(cat "$PID_FILE" 2>/dev/null); do
    kill "$pid" 2>/dev/null || true
done
sleep 1

echo "🚀 Starting Alpha Network services..."

# Kill any stale processes (by PID file)
for f in /var/run/alpha-network-node.pid /var/run/alpha-network-explorer.pid; do
    if [ -f "$f" ]; then
        kill "$(cat "$f")" 2>/dev/null || true
        sleep 1
        rm -f "$f"
    fi
done

# Also try killing by binary name
killall alphanode 2>/dev/null || true
killall explorer 2>/dev/null || true
sleep 1

# Start the chain node
nohup "$NODE_BIN" \
    -datadir="$DATA_DIR" \
    -port="$NODE_PORT" \
    -ws-port="$WS_PORT" \
    > "$LOG_DIR/alphanode.log" 2>&1 &
NODE_PID=$!
echo "$NODE_PID" > /var/run/alpha-network-node.pid
echo "  alphanode PID: $NODE_PID"

# Wait for node to be ready
sleep 2

# Start the block explorer
nohup "$EXPLORER_BIN" \
    -addr=":$EXPLORER_PORT" \
    -api="http://localhost:$NODE_PORT" \
    -ws="ws://localhost:$WS_PORT" \
    > "$LOG_DIR/alpha-explorer.log" 2>&1 &
EXPLORER_PID=$!
echo "$EXPLORER_PID" > /var/run/alpha-network-explorer.pid
echo "  explorer PID: $EXPLORER_PID"

# Verify they're running
sleep 1
if kill -0 "$NODE_PID" 2>/dev/null; then
    echo "✅ alphanode running (PID $NODE_PID)"
else
    echo "❌ alphanode failed to start — check $LOG_DIR/alphanode.log"
fi
if kill -0 "$EXPLORER_PID" 2>/dev/null; then
    echo "✅ explorer running (PID $EXPLORER_PID)"
else
    echo "❌ explorer failed to start — check $LOG_DIR/alpha-explorer.log"
fi

echo "📡 Node API:    http://localhost:$NODE_PORT"
echo "📡 WebSocket:   ws://localhost:$WS_PORT"
echo "🌐 Explorer:    http://localhost:$EXPLORER_PORT"
echo "📋 Logs:        $LOG_DIR/alphanode.log"
echo "                $LOG_DIR/alpha-explorer.log"
