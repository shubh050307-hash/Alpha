#!/bin/bash
# deploy-website.sh — Copy website files to /var/www/alphanetx and reload Caddy
# Usage: ./deploy-website.sh
# Also run by inotifywatch on file changes in website/

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SOURCE_DIR="$PROJECT_DIR/website"
TARGET_DIR="/var/www/alphanetx"

echo "📦 Deploying website..."

# Verify source exists
if [ ! -d "$SOURCE_DIR" ]; then
    echo "❌ Source directory not found: $SOURCE_DIR"
    exit 1
fi

# Create target if needed
mkdir -p "$TARGET_DIR"

# Copy all website files (skip serve.go and .bak files)
cp "$SOURCE_DIR"/*.html "$TARGET_DIR/" 2>/dev/null || true
cp "$SOURCE_DIR"/*.css "$TARGET_DIR/" 2>/dev/null || true
cp "$SOURCE_DIR"/*.js "$TARGET_DIR/" 2>/dev/null || true
cp "$SOURCE_DIR"/*.png "$TARGET_DIR/" 2>/dev/null || true
cp "$SOURCE_DIR"/*.ico "$TARGET_DIR/" 2>/dev/null || true
cp "$SOURCE_DIR"/*.svg "$TARGET_DIR/" 2>/dev/null || true

# Reload Caddy gracefully
if command -v caddy &>/dev/null; then
    if caddy reload --config /etc/caddy/Caddyfile 2>/dev/null; then
        echo "✅ Website deployed, Caddy reloaded"
    else
        echo "⚠️  Files copied, Caddy reload skipped (not running?)"
    fi
else
    echo "⚠️  Files copied, Caddy not installed"
fi

echo "   → $TARGET_DIR"
ls -la "$TARGET_DIR/"
