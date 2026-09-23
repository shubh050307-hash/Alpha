#!/bin/bash
# Alpha Network — Stop All Services
set -euo pipefail

echo "🔴 Stopping Alpha Network services..."

for pid_file in /var/run/alpha-network-node.pid /var/run/alpha-network-explorer.pid; do
    if [ -f "$pid_file" ]; then
        pid=$(cat "$pid_file" 2>/dev/null)
        if kill "$pid" 2>/dev/null; then
            echo "  Stopped PID $pid (${pid_file##*/})"
        fi
        rm -f "$pid_file"
    fi
done

# Clean up killall as well
killall alphanode explorer 2>/dev/null || true

echo "✅ All services stopped"
