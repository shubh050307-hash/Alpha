#!/bin/bash
# Alpha Network Health Monitor
# Run every 5 minutes via cron
# Checks health endpoints and restarts processes if down
set -euo pipefail

NODE_HEALTH_URL="http://localhost:8080/health"
EXPLORER_URL="http://localhost:8082/"
LOG_FILE="/var/log/alpha-monitor.log"
FAILURE_FILE_NODE="/tmp/alpha-monitor-failures-nodes"
FAILURE_FILE_EXPL="/tmp/alpha-monitor-failures-expl"
MAX_FAILURES=2

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

alive() {
    # True if process exists (by PID file) AND port responds
    local name=$1
    local pid_file=$2
    local url=$3
    if [ -f "$pid_file" ]; then
        local pid
        pid=$(cat "$pid_file" 2>/dev/null)
        if [ -d "/proc/$pid" ] 2>/dev/null && curl -sf --max-time 5 "$url" > /dev/null 2>&1; then
            return 0
        fi
    fi
    # Try direct health check (process may be running without PID file)
    if curl -sf --max-time 5 "$url" > /dev/null 2>&1; then
        return 0
    fi
    return 1
}

start_node() {
    nohup /opt/Alpha-Network/alphanode \
        -datadir=/root/.alpha \
        -port=8080 \
        -ws-port=8081 \
        > /var/log/alphanode.log 2>&1 &
    local pid=$!
    echo "$pid" > /var/run/alpha-network-node.pid
    log "🚀 Started alphanode (PID $pid)"
    sleep 3
}

start_explorer() {
    nohup /opt/Alpha-Network/explorer/explorer \
        -addr=:8082 \
        -api=http://localhost:8080 \
        -ws=ws://localhost:8081 \
        > /var/log/alpha-explorer.log 2>&1 &
    local pid=$!
    echo "$pid" > /var/run/alpha-network-explorer.pid
    log "🚀 Started explorer (PID $pid)"
    sleep 2
}

log "=== Alpha Monitor: checking services ==="

# Check alphanode
if alive "alphanode" "/var/run/alpha-network-node.pid" "$NODE_HEALTH_URL"; then
    rm -f "$FAILURE_FILE_NODE"
    log "✅ alphanode healthy"
else
    f=0
    [ -f "$FAILURE_FILE_NODE" ] && f=$(cat "$FAILURE_FILE_NODE")
    f=$((f + 1))
    echo "$f" > "$FAILURE_FILE_NODE"
    log "⚠️ alphanode FAILURE $f/$MAX_FAILURES"

    if [ "$f" -ge "$MAX_FAILURES" ]; then
        log "🔴 Restarting alphanode..."
        kill "$(cat /var/run/alpha-network-node.pid 2>/dev/null)" 2>/dev/null || true
        sleep 1
        start_node
        rm -f "$FAILURE_FILE_NODE"
    fi
fi

# Check explorer (only if node is healthy)
if [ -f /var/run/alpha-network-node.pid ] || curl -sf --max-time 3 "$NODE_HEALTH_URL" > /dev/null 2>&1; then
    if alive "explorer" "/var/run/alpha-network-explorer.pid" "$EXPLORER_URL"; then
        rm -f "$FAILURE_FILE_EXPL"
        log "✅ explorer healthy"
    else
        f=0
        [ -f "$FAILURE_FILE_EXPL" ] && f=$(cat "$FAILURE_FILE_EXPL")
        f=$((f + 1))
        echo "$f" > "$FAILURE_FILE_EXPL"
        log "⚠️ explorer FAILURE $f/$MAX_FAILURES"

        if [ "$f" -ge "$MAX_FAILURES" ]; then
            log "🔴 Restarting explorer..."
            kill "$(cat /var/run/alpha-network-explorer.pid 2>/dev/null)" 2>/dev/null || true
            sleep 1
            start_explorer
            rm -f "$FAILURE_FILE_EXPL"
        fi
    fi
else
    log "⏸️ Skipping explorer check — alphanode is down"
fi

log "=== Monitor cycle complete ==="
exit 0
