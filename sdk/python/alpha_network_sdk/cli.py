#!/usr/bin/env python3
"""
Alpha Network Agent CLI — Command-line interface for managing AI agents.

Usage:
    alpha-agent challenges              List available tasks/challenges
    alpha-agent start --model template  Start an agent (template/inference/validator/trader)
    alpha-agent start --model inference --duration 60
    alpha-agent balance --node http://localhost:8080
    alpha-agent info                    Show chain info
"""

from __future__ import annotations

import argparse
import os
import signal
import sys
import threading
import time
from typing import Optional


def _get_node_url(args_node: Optional[str] = None) -> str:
    """Resolve node URL from args, env, or default."""
    if args_node:
        return args_node
    return os.environ.get("ALPHA_API_URL", "http://localhost:8080")


# ── Commands ───────────────────────────────────────────────────────────────────


def cmd_challenges(args: argparse.Namespace) -> None:
    """List available challenges from the task marketplace."""
    from alpha_network_sdk.agent import run_challenge_mode

    node_url = _get_node_url(args.node)
    run_challenge_mode(
        node_url=node_url,
        capability=args.capability,
        max_challenges=args.limit,
    )


def cmd_start(args: argparse.Namespace) -> None:
    """Start an agent with a given model preset."""
    from alpha_network_sdk.agent import create_agent, MODEL_PRESETS

    if args.model not in MODEL_PRESETS:
        valid = ", ".join(MODEL_PRESETS)
        print(f"❌ Unknown model '{args.model}'. Choose from: {valid}")
        sys.exit(1)

    node_url = _get_node_url(args.node)
    preset = MODEL_PRESETS[args.model]

    print(f"⚡ Starting {args.model} agent ({preset['description']})")
    print(f"   Node:     {node_url}")
    print(f"   Duration: {args.duration}s")
    print()

    agent = create_agent(
        model=args.model,
        address=args.address,
        node_url=node_url,
        register=not args.no_register,
    )

    # Track stats
    start_time = time.time()
    start_balance = 0
    try:
        start_balance = agent.balance()
    except Exception:
        pass

    print(f"   Starting balance: {start_balance} $ALPHA")

    can_earn = not args.no_earn and agent.agent_id() is not None
    if can_earn:
        agent.start_earning()
        print()
        print(f"⚡ Agent is now earning $ALPHA (running for {args.duration}s)...")
        print(f"   Press Ctrl+C to stop early")
    elif not args.no_earn:
        print()
        print(f"⚠️  Earning skipped — agent not registered (insufficient stake $ALPHA?)")

    # Run for duration
    shutdown = threading.Event()

    def _on_signal(sig, frame):
        print("\n⚠️  Shutdown signal received…")
        shutdown.set()

    signal.signal(signal.SIGINT, _on_signal)
    signal.signal(signal.SIGTERM, _on_signal)

    remaining = args.duration
    tick = 1
    while remaining > 0 and not shutdown.is_set():
        time.sleep(1)
        remaining -= 1
        if tick % 10 == 0 and remaining > 0:
            try:
                info = agent.chain_info()
                bal = agent.balance()
                print(f"   [{args.duration - remaining}s] Height: {info.get('height','?')} | Balance: {bal} $ALPHA")
            except Exception:
                pass
        tick += 1

    if can_earn:
        agent.stop_earning()

    # Final stats
    print()
    try:
        final_balance = agent.balance()
        elapsed = time.time() - start_time
        print(f"   ✅ Test completed after {elapsed:.1f}s")
        print(f"   Final balance:  {final_balance} $ALPHA")
    except Exception as exc:
        print(f"   ⚠️  Final balance check failed: {exc}")

    print()
    print(f"   Model:       {args.model}")
    print(f"   Agent ID:    {agent.agent_id() or 'not registered'}")
    print(f"   Address:     {agent.address}")


def cmd_balance(args: argparse.Namespace) -> None:
    """Check balance for an address."""
    from alpha_network_sdk import AlphaClient

    node_url = _get_node_url(args.node)
    client = AlphaClient(node_url)

    try:
        resp = client.get_balance(args.address)
        print(f"⚡ Balance: {resp.get('balance', 0)} $ALPHA")
        print(f"   Address: {args.address}")
    except Exception as exc:
        print(f"❌ Failed: {exc}")
        sys.exit(1)


def cmd_info(args: argparse.Namespace) -> None:
    """Show chain info."""
    from alpha_network_sdk import AlphaClient

    node_url = _get_node_url(args.node)
    client = AlphaClient(node_url)

    try:
        info = client.chain_info()
        print("⚡ Alpha Network Chain Info")
        for k, v in info.items():
            print(f"   {k}: {v}")

        # If --address provided, show rewards
        if args.address:
            rewards = client.get_rewards(args.address)
            print(f"\n💰 Total Earned: {rewards['total_earned']} $ALPHA")
            print(f"   Rewards Count: {rewards['count']}")
            if rewards['rewards']:
                print("   Recent rewards:")
                for r in rewards['rewards'][:5]:
                    print(f"     • {r['amount']} $ALPHA — Rank {r['rank']} — {r['challenge_id']}")
    except Exception as exc:
        print(f"❌ Failed: {exc}")
        sys.exit(1)


# ── Main ───────────────────────────────────────────────────────────────────────


def main(argv: Optional[list] = None) -> None:
    parser = argparse.ArgumentParser(
        prog="alpha-agent",
        description="Alpha Network Agent CLI — manage AI agents on Alpha Network",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  alpha-agent challenges
  alpha-agent start --model template
  alpha-agent start --model inference --node https://alphanetx.xyz --duration 120
  alpha-agent balance --address alpha1abc...
  alpha-agent info
        """,
    )

    sub = parser.add_subparsers(dest="command", help="Available commands")

    # ── challenges ──
    p_ch = sub.add_parser("challenges", help="List available tasks/challenges")
    p_ch.add_argument("--node", default=None, help="Node URL (default: $ALPHA_API_URL or http://localhost:8080)")
    p_ch.add_argument("--capability", default=None, help="Filter by capability (inference, validation, etc.)")
    p_ch.add_argument("--limit", type=int, default=20, help="Max challenges to show")

    # ── start ──
    p_start = sub.add_parser("start", help="Start an AI agent")
    p_start.add_argument("--model", default="template", help="Model preset: template, inference, validator, trader")
    p_start.add_argument("--node", default=None, help="Node URL")
    p_start.add_argument("--address", default=None, help="Agent address (auto-generated if omitted)")
    p_start.add_argument("--duration", type=int, default=60, help="Run duration in seconds (default: 60)")
    p_start.add_argument("--no-register", action="store_true", help="Skip on-chain registration")
    p_start.add_argument("--no-earn", action="store_true", help="Skip earning loop")

    # ── balance ──
    p_bal = sub.add_parser("balance", help="Check $ALPHA balance")
    p_bal.add_argument("address", help="Alpha Network address")
    p_bal.add_argument("--node", default=None, help="Node URL")

    # ── info ──
    p_info = sub.add_parser("info", help="Show chain info")
    p_info.add_argument("--node", default=None, help="Node URL")
    p_info.add_argument("--address", default=None, help="Agent address (show rewards)")

    args = parser.parse_args(argv)

    if args.command is None:
        parser.print_help()
        sys.exit(0)

    # Dispatch
    commands = {
        "challenges": cmd_challenges,
        "start": cmd_start,
        "balance": cmd_balance,
        "info": cmd_info,
    }

    fn = commands.get(args.command)
    if fn:
        fn(args)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
