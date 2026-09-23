# Contributing to Alpha Network

Alpha Network is an open-source, community-driven protocol. All contributions are welcome.

## Getting Started

1. **Fork** the repository
2. **Clone** your fork: `git clone https://github.com/YOUR_USERNAME/Alpha-Network.git`
3. **Create a branch**: `git checkout -b feature/your-feature`
4. **Make changes**, test, document
5. **Push** to your fork and open a **Pull Request**

## Development Requirements

- **Go 1.24+** — [Download](https://go.dev/dl/)
- **Python 3.12+** (for SDK development)
- `make` (optional, for convenience commands)

## Code Quality

All Go code must pass:

```bash
go build ./...     # Compiles cleanly
go vet ./...       # No vet warnings
go test ./...      # All tests pass
go fmt ./...       # Properly formatted
```

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add ZK proof verification for task submissions
fix: correct balance seeding for demo agents
docs: update API reference with new endpoints
test: add integration test for SDK client
```

## Project Structure

| Directory | Description |
|---|---|
| `chain/` | Core blockchain implementation |
| `chain/core/` | Fundamental types (Block, Transaction, Address) |
| `chain/consensus/` | Proof of Intelligence consensus engine |
| `chain/agent/` | Agent registry and reputation |
| `chain/ledger/` | Token accounting and burns |
| `chain/producer/` | Block production loop |
| `chain/api/` | REST API and WebSocket server |
| `chain/data/` | Intelligence oracle and data marketplace |
| `chain/tasks/` | Task marketplace |
| `chain/store/` | BadgerDB persistence layer |
| `chain/crypto/` | Cryptography (Bech32, ZK proofs) |
| `chain/p2p/` | P2P networking (Phase 4) |
| `chain/sync/` | Block synchronization (Phase 4) |
| `sdk/` | Language SDKs |
| `explorer/` | Block explorer |
| `website/` | Landing page and documentation |
| `docs/` | Developer documentation |
| `scripts/` | Helper and deployment scripts |

## Feature Requests & Bug Reports

- **Feature requests:** Open an issue with the `enhancement` label
- **Bug reports:** Open an issue with the `bug` label, include:
  - Steps to reproduce
  - Expected vs actual behavior
  - Node version (`alphanode --help` shows version)
  - Relevant logs

## Pull Request Process

1. Update documentation if your change affects user-facing behavior
2. Add tests for new functionality
3. Ensure CI passes (build, test, vet)
4. Request review from maintainers
5. Address review feedback

## Code of Conduct

- Be respectful. Disagree constructively.
- Keep the protocol anonymous — no real names in code or comments
- Production quality only — no TODOs, no placeholders in merged code

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
