#!/bin/bash
# watch-website.sh — Watch website/ directory for changes, auto-deploy
# Runs as a systemd service (alpha-website-watcher.service)

WEBSITE_DIR="/opt/Alpha-Network/website"
DEPLOY_SCRIPT="/opt/Alpha-Network/scripts/deploy-website.sh"

# Debounce: wait for writes to settle before deploying
while true; do
    inotifywait -e modify -e create -e delete -e move -r "$WEBSITE_DIR" 2>/dev/null
    # Wait for writes to settle (debounce 500ms)
    sleep 0.5
    "$DEPLOY_SCRIPT" 2>&1 | logger -t alpha-website-watcher
done
