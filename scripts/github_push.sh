#!/usr/bin/env bash
# Alpha Network — anonymous GitHub setup
# Prepares a local git repository for publishing to GitHub.
# Does NOT run git push — you do that manually (see instructions below).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "⚡ Alpha Network — anonymous GitHub setup"
echo "   Repository: $REPO_DIR"
echo ""

# ── Initialize git repo ───────────────────────────────────────────────────────
cd "$REPO_DIR"

if [ ! -d ".git" ]; then
    git init
    echo "✅ Initialized git repository"
else
    echo "✅ Git repository already initialized"
fi

# ── Create .gitignore ─────────────────────────────────────────────────────────
cat > .gitignore <<'EOF'
# Alpha Network data directories
.alpha/
.alpha_data/

# BadgerDB files
*.db
*.vlog
*.sst

# Go build artifacts
alphanode
alphachain
explorer/explorer
*.exe
*.out

# Python
__pycache__/
*.pyc
*.pyo
*.pyd
.env
venv/
.venv/

# Go module vendor directory
vendor/

# OS
.DS_Store
Thumbs.db

# Editor
.vscode/
.idea/
*.swp
*.swo
EOF
echo "✅ Created .gitignore"

# ── Create LICENSE (MIT, anonymous) ──────────────────────────────────────────
YEAR=$(date +%Y)
cat > LICENSE <<EOF
MIT License

Copyright (c) $YEAR Alpha Network Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
EOF
echo "✅ Created LICENSE (MIT, Alpha Network Contributors)"

# ── Configure anonymous git identity (local only) ─────────────────────────────
git config user.name  "Alpha Network Contributors"
git config user.email "anon@alpha.network"
echo "✅ Set anonymous git identity (local config only)"

# ── Stage all files ───────────────────────────────────────────────────────────
git add -A
echo "✅ Staged all files"

# ── Create initial commit ─────────────────────────────────────────────────────
if git diff --cached --quiet; then
    echo "ℹ  Nothing new to commit (working tree is clean)"
else
    git commit -m "Alpha Network v0.3 — AI agent blockchain"
    echo "✅ Initial commit created"
fi

# ── Print anonymous push instructions ────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════════════════"
echo "  📦 Repository is ready. To push to GitHub anonymously:"
echo "════════════════════════════════════════════════════════════════"
echo ""
echo "  Step 1: Create a new GitHub account with no real name / email."
echo "          Use a temporary email (e.g. SimpleLogin, AnonAddy)."
echo "          Enable 2FA immediately."
echo ""
echo "  Step 2: Create a new repository on GitHub:"
echo "          Name: alpha-network  (or whatever you prefer)"
echo "          Visibility: Public"
echo "          Do NOT initialize with README (you already have one)"
echo ""
echo "  Step 3: Add the remote and push:"
echo "          git remote add origin https://github.com/YOUR_USERNAME/alpha-network.git"
echo "          git branch -M main"
echo "          git push -u origin main"
echo ""
echo "  Step 4 (optional): For extra anonymity, push over Tor:"
echo "          torify git push -u origin main"
echo "          Or use a VPN / residential proxy."
echo ""
echo "  ⚠  Do NOT use your real GitHub account or real email."
echo "     Your commit identity is set to 'Alpha Network Contributors'."
echo "     GitHub still logs your IP — use Tor or VPN for full anonymity."
echo ""
echo "════════════════════════════════════════════════════════════════"
