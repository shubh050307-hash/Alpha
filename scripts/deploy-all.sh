#!/bin/bash
# deploy-all.sh — Full deployment: build, deploy, verify
# Usage: bash deploy-all.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_DIR"

echo "═══════════════════════════════════════"
echo "  ALPHA NETWORK — FULL DEPLOY"
echo "═══════════════════════════════════════"
echo ""

# ── 1. Build alphanode ─────────────────────────────────────────────────────
echo "🔨 Building alphanode..."
go build -o alphanode . 2>&1
echo "   alphanode: $(ls -lh alphanode | awk '{print $5}')"

# ── 2. Build explorer ──────────────────────────────────────────────────────
echo "🔨 Building explorer..."
cd explorer && go build -o explorer . 2>&1 && cd ..
echo "   explorer: $(ls -lh explorer/explorer | awk '{print $5}')"

# ── 3. Deploy website ──────────────────────────────────────────────────────
echo "📦 Deploying website..."
cp website/*.html /var/www/alphanetx/ 2>/dev/null || true
cp website/*.css /var/www/alphanetx/ 2>/dev/null || true
cp website/*.js /var/www/alphanetx/ 2>/dev/null || true
echo "   → /var/www/alphanetx/"

# ── 4. Restart services ────────────────────────────────────────────────────
echo "🔄 Restarting services..."

# Explorer
kill $(lsof -ti:8082 2>/dev/null) 2>/dev/null || true
sleep 1
nohup "$PROJECT_DIR/explorer/explorer" --addr :8082 --api http://localhost:8080 &>/var/log/alpha-explorer.log &
echo "   Explorer started on :8082"

# Homepage static server
kill $(lsof -ti:3003 2>/dev/null) 2>/dev/null || true
sleep 1
nohup python3 -m http.server 3003 -d /var/www/alphanetx &>/dev/null &
echo "   Static server started on :3003"

# Caddy — start fresh if not running, reload if running
if command -v caddy &>/dev/null; then
    if caddy reload --config /etc/caddy/Caddyfile 2>/dev/null; then
        echo "   Caddy reloaded"
    else
        # Not running — start it
        kill $(lsof -ti:80 2>/dev/null) 2>/dev/null || true
        sleep 1
        nohup caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &>/var/log/caddy.log &
        sleep 2
        echo "   Caddy started on :80"
    fi
else
    echo "   ⚠️  Caddy not found"
fi

sleep 2

# ── 5. Verify ───────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════"
echo "  VERIFICATION"
echo "═══════════════════════════════════════"

check() {
    local code=$(curl -s -o /dev/null -w "%{http_code}" "$1" 2>/dev/null)
    if [ "$code" = "200" ]; then
        echo "  ✅ $2 → 200"
    else
        echo "  ❌ $2 → $code"
    fi
}

check "http://localhost:8082/" "Explorer dashboard"
check "http://localhost:8082/blocks" "Explorer blocks"
check "http://localhost:8082/agents" "Explorer agents"
check "http://localhost:8082/tasks" "Explorer tasks"
check "http://localhost:8082/intelligence" "Explorer intelligence"
check "http://localhost:3003/" "Homepage static"

echo ""
echo "═══ Deploy complete ═══"
