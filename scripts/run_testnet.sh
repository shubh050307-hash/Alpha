#!/usr/bin/env bash
# Alpha Network — one-command testnet launcher
# Usage: ./scripts/run_testnet.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ALPHA_DIR="$HOME/.alpha"
CHAIN_BIN="$PROJECT_ROOT/alphanode"
EXPLORER_BIN="$PROJECT_ROOT/explorer/explorer"
EXPLORER_DIR="$PROJECT_ROOT/explorer"

# ── Build chain binary ────────────────────────────────────────────────────────
echo "⚙  Building Alpha Network node..."
cd "$PROJECT_ROOT"
go build -o "$CHAIN_BIN" . 2>&1
echo "✅ Chain binary built: $CHAIN_BIN"

# ── Build explorer binary ─────────────────────────────────────────────────────
echo "⚙  Building block explorer..."
cd "$EXPLORER_DIR"
go build -o "$EXPLORER_BIN" . 2>&1
echo "✅ Explorer binary built: $EXPLORER_BIN"

# ── Create data directory ─────────────────────────────────────────────────────
mkdir -p "$ALPHA_DIR/data"
echo "📁 Data directory: $ALPHA_DIR"

# ── Cleanup handler ───────────────────────────────────────────────────────────
NODE_PID=""
EXPLORER_PID=""

cleanup() {
    echo ""
    echo "🛑 Shutting down Alpha Network testnet..."
    [ -n "$EXPLORER_PID" ] && kill "$EXPLORER_PID" 2>/dev/null || true
    [ -n "$NODE_PID" ]     && kill "$NODE_PID"     2>/dev/null || true
    echo "✅ Stopped."
    exit 0
}
trap cleanup SIGINT SIGTERM

# ── Start node ────────────────────────────────────────────────────────────────
echo ""
echo "🔷 Starting Alpha Network node..."
"$CHAIN_BIN" --datadir "$ALPHA_DIR" --port 8080 --ws-port 8081 &
NODE_PID=$!

# Wait for node to become healthy
echo "⏳ Waiting for node to be ready..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:8080/health >/dev/null 2>&1; then
        echo "✅ Node is healthy"
        break
    fi
    sleep 1
done

# ── Start explorer ────────────────────────────────────────────────────────────
echo "🌐 Starting block explorer..."
"$EXPLORER_BIN" --addr :8082 --api http://localhost:8080 --ws ws://localhost:8081 &
EXPLORER_PID=$!

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║      Alpha Network testnet running                           ║"
echo "║                                                              ║"
echo "║   API:      http://localhost:8080                            ║"
echo "║   Explorer: http://localhost:8082                            ║"
echo "║   WS:       ws://localhost:8081/ws                           ║"
echo "║                                                              ║"
echo "║   Press Ctrl+C to stop                                       ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# Keep running until signal
wait
