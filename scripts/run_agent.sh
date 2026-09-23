#!/usr/bin/env bash
# Alpha Network — one-command agent launcher
# Usage: ./scripts/run_agent.sh [API_URL]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT_SCRIPT="$PROJECT_ROOT/sdk/python/example_agent.py"
API_URL="${1:-http://localhost:8080}"

# ── Check Python 3 ────────────────────────────────────────────────────────────
if ! command -v python3 &>/dev/null; then
    echo "❌ Python 3 is required but not found."
    echo "   Install: https://www.python.org/downloads/"
    exit 1
fi

PYTHON_VER=$(python3 --version 2>&1 | awk '{print $2}')
echo "✅ Python $PYTHON_VER found"

# ── Check requests library ────────────────────────────────────────────────────
if ! python3 -c "import requests" 2>/dev/null; then
    echo "📦 Installing 'requests' library..."
    pip3 install requests --quiet
fi
echo "✅ requests library available"

# ── Check agent script ────────────────────────────────────────────────────────
if [ ! -f "$AGENT_SCRIPT" ]; then
    echo "❌ Agent script not found: $AGENT_SCRIPT"
    exit 1
fi

# ── Check node availability ───────────────────────────────────────────────────
if ! curl -sf "$API_URL/health" >/dev/null 2>&1; then
    echo "⚠  Warning: Alpha Network node not responding at $API_URL"
    echo "   Start the node first: ./scripts/run_testnet.sh"
    echo ""
    echo "Continuing anyway — the agent will retry on connect errors..."
    echo ""
fi

# ── Launch agent ──────────────────────────────────────────────────────────────
echo ""
echo "🤖 Launching Alpha Network agent..."
echo "   Node:   $API_URL"
echo "   Script: $AGENT_SCRIPT"
echo ""

export ALPHA_API_URL="$API_URL"
python3 "$AGENT_SCRIPT"
