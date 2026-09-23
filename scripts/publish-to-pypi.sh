#!/bin/bash
# publish-to-pypi.sh — Build and publish the Python SDK to PyPI
# Usage: ./publish-to-pypi.sh
#
# Prerequisites:
#   1. Create a PyPI account at https://pypi.org/account/register/
#      (Use any email — the goal is functional anonymity, not PyPI account traceability)
#   2. Create an API token at https://pypi.org/manage/account/token/
#      Token scope: "Entire account (all projects)"
#      Token name: "alpha-network-sdk-publish"
#   3. Run this script — it will prompt for the token
#
# One-time command:
#   TWINE_USERNAME=__token__ TWINE_PASSWORD=pypi-xxxxxxxx python3 -m twine upload dist/*

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SDK_DIR="$(cd "$SCRIPT_DIR/../sdk/python" && pwd)"

echo "📦 Building alpha-network-sdk..."
cd "$SDK_DIR"

# Clean old dist
rm -rf dist/ build/ *.egg-info

# Activate venv and build
if [ -f ".venv/bin/activate" ]; then
    source .venv/bin/activate
fi

python3 -m build
echo ""
echo "✅ Build complete!"
echo ""
echo "   Files:"
ls -la dist/
echo ""
echo "🚀 Ready to publish!"
echo ""
echo "   Run the following command (paste your PyPI token when prompted):"
echo ""
echo "   TWINE_USERNAME=__token__ \\"
echo "   TWINE_PASSWORD=\$(read -sp 'PyPI token: ' tok; echo \$tok) \\"
echo "   python3 -m twine upload dist/*"
echo ""
echo "   Or with inline token:"
echo ""
echo "   TWINE_USERNAME=__token__ TWINE_PASSWORD='pypi-xxxxx' python3 -m twine upload dist/*"
echo ""
echo "📝 Anonymous publishing notes:"
echo "   - Use a disposable email (ProtonMail, TempMail) for PyPI registration"
echo "   - The package name 'alpha-network-sdk' doesn't identify anyone"
echo "   - No real name required in PyPI registration"
echo "   - All code, docs, and metadata use 'Alpha Network Contributors'"
echo ""
