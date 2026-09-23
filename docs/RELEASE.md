# Alpha Network Release Checklist

## Creating a Release

1. Update version strings:
   - `chain/api/server.go` — version field in `/chain/info` and `/health`
   - `sdk/python/alpha_sdk.py` — `__version__`
   - `whitepaper/ALPHA_WHITEPAPER.md` — version header

2. Update CHANGELOG.md with release notes

3. Tag and push:
```bash
git tag -a v0.4.0 -m "Release v0.4.0 — description"
git push origin v0.4.0
```

4. Create GitHub Release from the tag with:
   - Release notes
   - Pre-built binaries (Linux amd64, arm64, macOS)

## Building Release Binaries

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o alphanode-linux-amd64 .

# Linux arm64
GOOS=linux GOARCH=arm64 go build -o alphanode-linux-arm64 .

# macOS amd64
GOOS=darwin GOARCH=amd64 go build -o alphanode-darwin-amd64 .

# macOS arm64
GOOS=darwin GOARCH=arm64 go build -o alphanode-darwin-arm64 .
```

## Pre-Release Checklist

- [ ] All tests pass: `go test ./...`
- [ ] No vet warnings: `go vet ./...`
- [ ] Build clean: `go build ./...`
- [ ] API version updated
- [ ] SDK version updated
- [ ] Whitepaper version updated
- [ ] CHANGELOG.md updated
- [ ] CI/CD green
- [ ] Node smoke tested locally
- [ ] Explorer tested
