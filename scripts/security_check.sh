#!/bin/bash
set -euo pipefail

REPO="/opt/Alpha-Network"
FAILED=0
SELF="scripts/security_check.sh"

# Check for identity leaks in staged diff
PATTERNS=(
 "Zak_net"
 "@zak"
 "62.238.33.71"
 "GALAXIA"
 "Owner:"
 "private key"
 "PASSWORD"
 "API_KEY"
 "mnemonic"
 "seed phrase"
)

for pattern in "${PATTERNS[@]}"; do
  HITS=$(git -C "$REPO" diff --cached -i -G "$pattern" --name-only 2>/dev/null | grep -v "$SELF" | grep -v ".gitignore" || true)
  if [ -n "$HITS" ]; then
    echo "🔴 BLOCKED: Found '$pattern' in staged files: $HITS"
    FAILED=1
  fi
done

# Check for sensitive filenames being staged
SENSITIVE_FILES=(
 "GALAXIA.md"
 "MEMORY.md"
 "SOUL.md"
 ".env"
)

for file in "${SENSITIVE_FILES[@]}"; do
  if git -C "$REPO" diff --cached --name-only 2>/dev/null | grep -v "$SELF" | grep -v ".gitignore" | grep -q "$file"; then
    echo "🔴 BLOCKED: Sensitive file staged: $file"
    FAILED=1
  fi
done

if [ "$FAILED" -eq 1 ]; then
  echo ""
  echo "❌ Security check FAILED — push blocked"
  echo "Remove sensitive content before pushing"
  exit 1
fi

echo "✅ Security check passed — safe to push"
exit 0
