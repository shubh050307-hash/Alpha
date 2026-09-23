#!/bin/bash
# start-webserver.sh — Start/restart the static file server
# Serves /var/www/alphanetx on port 3003

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVER_BIN="$SCRIPT_DIR/alpha-web-server"

# Kill existing instance
pkill -f "alpha-web-server" 2>/dev/null

# Wait for port to free up
sleep 1

# Start new instance
nohup "$SERVER_BIN" > /dev/null 2>&1 &
echo "✅ Static server started on port 3003 (PID $!)"
