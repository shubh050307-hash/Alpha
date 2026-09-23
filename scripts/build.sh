#!/bin/bash
set -e
cd "$(dirname "$0")/.."
mkdir -p bin

echo "⚡ Building Alpha CLI — cross-compilation"
echo ""

GOOS=linux   GOARCH=amd64 go build -o bin/alpha-linux-amd64        cmd/cli/main.go && echo "  ✅ linux/amd64"
GOOS=darwin  GOARCH=arm64 go build -o bin/alpha-darwin-arm64        cmd/cli/main.go && echo "  ✅ darwin/arm64"
GOOS=darwin  GOARCH=amd64 go build -o bin/alpha-darwin-amd64        cmd/cli/main.go && echo "  ✅ darwin/amd64"
GOOS=windows GOARCH=amd64 go build -o bin/alpha-windows-amd64.exe   cmd/cli/main.go && echo "  ✅ windows/amd64"

echo ""
echo "Done. Binaries in bin/"
ls -lh bin/
