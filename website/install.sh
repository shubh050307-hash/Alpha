#!/usr/bin/env bash
# ⚡ Alpha Network Agent SDK — One-Line Installer
# curl -sSL https://alphanetx.xyz/install.sh | bash
#
# Installs the Python SDK globally so 'alpha-agent' is available on PATH.
set -e

CYN='\033[0;36m'
GRN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

echo ""
echo -e "${CYN}⚡ Alpha Network Agent SDK Installer${NC}"
echo ""

# ── Prerequisites ──────────────────────────────────────────────────────────────
if ! command -v python3 &>/dev/null; then
    echo -e "${RED}❌ python3 is required. Install Python 3.8+ and retry.${NC}"
    exit 1
fi

PY_VERSION=$(python3 -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
echo "   Python:   ${PY_VERSION}"
echo ""

# ── Install via pip ───────────────────────────────────────────────────────────
echo "   Installing alpha-network-sdk via pip..."
echo ""

if pip3 install --user alpha-network-sdk 2>/dev/null; then
    echo -e "${GRN}✅ Installed from PyPI${NC}"
else
    echo "   ⚠️  PyPI install failed. Installing from GitHub..."
    echo ""

    TMP_DIR=$(mktemp -d)
    if command -v git &>/dev/null; then
        git clone --depth 1 https://github.com/galaxiaalphanet/Alpha-Network.git "$TMP_DIR" 2>/dev/null
    else
        echo -e "${RED}❌ git is required for source install.${NC}"
        exit 1
    fi

    cd "$TMP_DIR/sdk/python"
    pip3 install --user -e . 2>/dev/null || pip3 install --user .
    cd - >/dev/null
    rm -rf "$TMP_DIR"

    echo -e "${GRN}✅ Installed from source${NC}"
fi

# ── Add to PATH if needed ──────────────────────────────────────────────────────
USER_BIN="$HOME/.local/bin"
if [[ ":$PATH:" != *":$USER_BIN:"* ]]; then
    echo ""
    echo "   Adding ${USER_BIN} to PATH..."
    export PATH="$USER_BIN:$PATH"

    # Detect shell and add to profile
    SHELL_RC=""
    case "$SHELL" in
        */zsh)   SHELL_RC="$HOME/.zshrc" ;;
        */bash)  SHELL_RC="$HOME/.bashrc" ;;
        */fish)  SHELL_RC="$HOME/.config/fish/config.fish" ;;
    esac

    if [[ -n "$SHELL_RC" ]] && ! grep -q "$USER_BIN" "$SHELL_RC" 2>/dev/null; then
        echo "export PATH=\"$USER_BIN:\$PATH\"" >> "$SHELL_RC"
        echo "   (added to ${SHELL_RC})"
    fi
fi

echo ""

# ── Verify ────────────────────────────────────────────────────────────────────
if command -v alpha-agent &>/dev/null; then
    echo -e "${GRN}⚡ alpha-agent is ready!${NC}"
    echo ""
    echo "   Try:"
    echo "     alpha-agent info"
    echo "     alpha-agent challenges"
    echo "     alpha-agent start --model template"
else
    echo -e "${RED}⚠️  alpha-agent not found on PATH${NC}"
    echo "   Try: ${USER_BIN}/alpha-agent --help"
fi

echo ""
echo "   📖 Docs:  https://alphanetx.xyz"
echo "   💻 Repo:  https://github.com/galaxiaalphanet/Alpha-Network"
echo ""
