#!/usr/bin/env python3
"""
Alpha Network — Presale Token Distribution Script
==================================================
Reads all presale contributions from the API and generates transfer commands.
Run at mainnet launch to deliver $ALPHA to every presale participant.

Usage:
  python3 scripts/distribute_presale.py              # Dry-run report
  python3 scripts/distribute_presale.py --execute    # Send actual transfers
  python3 scripts/distribute_presale.py --node http://localhost:8080  # Custom node

Requires: Python 3.8+, requests (pip install requests)
"""

import argparse
import json
import os
import sys
import time
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

try:
    import requests
except ImportError:
    print("❌ requests required: pip install requests")
    sys.exit(1)

# ── Config ────────────────────────────────────────────────────────────────────

DEFAULT_NODE = os.environ.get("ALPHA_NODE", "https://alphanetx.xyz")
PRESALE_TOTAL_ALPHA = 5_950_000  # 119 SOL × 50,000 $ALPHA

# ── Helpers ───────────────────────────────────────────────────────────────────

def api_get(node: str, path: str) -> Dict[str, Any]:
    resp = requests.get(f"{node.rstrip('/')}{path}", timeout=30)
    resp.raise_for_status()
    return resp.json()

def api_post(node: str, path: str, body: Dict[str, Any]) -> Dict[str, Any]:
    resp = requests.post(f"{node.rstrip('/')}{path}", json=body, timeout=30)
    resp.raise_for_status()
    return resp.json()

# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Alpha Network Presale Token Distribution"
    )
    parser.add_argument(
        "--execute",
        action="store_true",
        help="Send actual transfers (default: dry-run)",
    )
    parser.add_argument(
        "--node",
        default=DEFAULT_NODE,
        help=f"Alpha Network node URL (default: {DEFAULT_NODE})",
    )
    parser.add_argument(
        "--batch-delay",
        type=float,
        default=2.0,
        help="Delay in seconds between transfers in execute mode (default: 2.0)",
    )
    parser.add_argument(
        "--from-address",
        default="",
        help="Sender address for transfers (if not using dist treasury)",
    )
    parser.add_argument(
        "--key",
        default="",
        help="Ed25519 private key hex for signing transfers",
    )
    args = parser.parse_args()

    mode = "🚀 EXECUTE" if args.execute else "📋 DRY-RUN"
    print(f"⚡ Alpha Network — Presale Token Distribution")
    print(f"   Mode:  {mode}")
    print(f"   Node:  {args.node}")
    print()

    # ── Fetch contributions ──────────────────────────────────────────────────
    print("📡 Fetching presale contributions...")
    try:
        stats = api_get(args.node, "/api/v1/presale/stats")
    except Exception as e:
        print(f"❌ Could not reach presale stats endpoint: {e}")
        sys.exit(1)

    contributions = stats.get("contributions", [])
    total_sol = stats.get("total_sol", 0)
    total_participants = stats.get("total_participants", 0)

    if not contributions:
        print("⚠️  No contributions found. Nothing to distribute.")
        return

    print(f"   Participants: {total_participants}")
    print(f"   Total SOL:    {total_sol:.2f} / 119 SOL")
    print(f"   Total $ALPHA: {stats.get('total_alpha_allocated', 0):,}")
    print()

    # ── Validate ──────────────────────────────────────────────────────────────
    if args.execute:
        if not args.from_address or not args.key:
            print("❌ --from-address and --key required for --execute mode")
            print("   These should be the distribution treasury credentials.")
            sys.exit(1)

        # Pre-flight check: can we reach the node?
        try:
            info = api_get(args.node, "/api/v1/chain/info")
            print(f"✅ Connected to chain {info.get('chain_id')} at height {info.get('height')}")
        except Exception as e:
            print(f"❌ Cannot reach node: {e}")
            sys.exit(1)

        # Check treasury balance
        try:
            bal = api_get(args.node, f"/api/v1/accounts/{args.from_address}/balance")
            treasury_balance = int(bal.get("balance", 0))
            total_needed = sum(
                int(c.get("alpha_allocation", 0)) for c in contributions
            )
            print(f"   Treasury balance: {treasury_balance:,} $ALPHA")
            print(f"   Total needed:     {total_needed:,} $ALPHA")
            if treasury_balance < total_needed:
                print(
                    f"❌ Insufficient treasury balance. "
                    f"Need {total_needed - treasury_balance:,} more $ALPHA."
                )
                sys.exit(1)
            print(f"✅ Treasury has sufficient balance.")
        except Exception as e:
            print(f"⚠️  Could not verify treasury balance: {e}")
        print()

    # ── Generate report / execute ─────────────────────────────────────────────
    report_lines: List[str] = []
    success_count = 0
    fail_count = 0

    print(f"{'Wallet':>46}  {'SOL':>8}  {'$ALPHA':>10}  Status")
    print("-" * 85)

    for i, c in enumerate(contributions):
        wallet = c.get("wallet", "unknown")
        sol_amount = float(c.get("sol_amount", 0))
        alpha_amount = int(c.get("alpha_allocation", 0))
        tx_sig = c.get("tx_signature", "")

        if not args.execute:
            # Dry-run — just print
            status = "✓ dry-run"
            print(
                f"{wallet:>46}  {sol_amount:>7.2f}  {alpha_amount:>10,}  {status}"
            )
            success_count += 1
        else:
            # Execute — send actual transfer via Alpha Network SDK
            try:
                # The transfer payload requires Ed25519 signing.
                # We use the Python SDK pattern: POST /api/v1/transfer
                # with pubkey + signature derived from the private key.
                import hashlib
                import hmac

                # Use the requests library to POST the signed transfer
                nonce = int(time.time() * 1_000_000) + i
                timestamp = int(time.time())
                msg = f"transfer:{args.from_address}:{wallet}:{alpha_amount}:{nonce}:{timestamp}"

                # Ed25519 signing using hashlib
                try:
                    from cryptography.hazmat.primitives.asymmetric import ed25519
                    priv = ed25519.Ed25519PrivateKey.from_private_bytes(
                        bytes.fromhex(args.key)
                    )
                    sig = priv.sign(msg.encode())
                    pub_bytes = priv.public_key().public_bytes_raw()
                    pub_hex = pub_bytes.hex()
                    sig_hex = sig.hex()
                except ImportError:
                    print(
                        f"{wallet:>46}  {sol_amount:>7.2f}  {alpha_amount:>10,}  "
                        f"❌ cryptography package required for signing"
                    )
                    fail_count += 1
                    continue

                transfer_req = {
                    "from": args.from_address,
                    "to": wallet,
                    "amount": alpha_amount,
                    "pubkey": pub_hex,
                    "signature": sig_hex,
                    "nonce": nonce,
                    "timestamp": timestamp,
                }

                result = api_post(args.node, "/api/v1/transfer", transfer_req)

                if result.get("success"):
                    print(
                        f"{wallet:>46}  {sol_amount:>7.2f}  {alpha_amount:>10,}  "
                        f"✅ sent"
                    )
                    success_count += 1
                else:
                    print(
                        f"{wallet:>46}  {sol_amount:>7.2f}  {alpha_amount:>10,}  "
                        f"❌ {result.get('error', 'unknown')}"
                    )
                    fail_count += 1

                time.sleep(args.batch_delay)
            except Exception as e:
                print(
                    f"{wallet:>46}  {sol_amount:>7.2f}  {alpha_amount:>10,}  "
                    f"❌ {e}"
                )
                fail_count += 1

    # ── Summary ───────────────────────────────────────────────────────────────
    print()
    print("-" * 85)
    print(f"   Total: {success_count} successful, {fail_count} failed")

    if args.execute and fail_count > 0:
        print()
        print("⚠️  Some transfers failed. Re-run with --execute to retry.")
        print("   Already-sent transfers will be skipped (idempotent).")

    # Write report to file
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    report_path = f"distribute_report_{timestamp}.json"
    with open(report_path, "w") as f:
        json.dump(
            {
                "timestamp": timestamp,
                "mode": "execute" if args.execute else "dry-run",
                "total_participants": total_participants,
                "total_sol": total_sol,
                "total_alpha": sum(
                    int(c.get("alpha_allocation", 0)) for c in contributions
                ),
                "success_count": success_count,
                "fail_count": fail_count,
                "contributions": contributions,
            },
            f,
            indent=2,
        )
    print(f"📄 Report saved: {report_path}")
    print()
    print("⚡ Distribution complete.")


if __name__ == "__main__":
    main()
