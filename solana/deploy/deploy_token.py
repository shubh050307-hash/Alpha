#!/usr/bin/env python3
"""
Alpha Network — SPL Token Deployment Script

Deploys $ALPHA token on Solana using the SPL Token program.
Supports both devnet and mainnet deployment.

Usage:
    python deploy_token.py --network devnet    # Test deployment
    python deploy_token.py --network mainnet   # Production deployment

Requirements:
    - Solana CLI installed
    - Keypair funded with SOL
    - spl-token CLI installed
"""

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

# ─── Configuration ──────────────────────────────────────────────────────────

TOKEN_CONFIG = {
    "name": "Alpha Network",
    "symbol": "ALPHA",
    "decimals": 9,
    "total_supply": 1_000_000_000,  # 1 billion
    "description": "The first blockchain built for AI agents",
    "image": "https://alphanetx.xyz/logo.png",
    "website": "https://alphanetx.xyz",
    "twitter": "https://twitter.com/alphalphanetx",
}

# Token allocation with vesting/lock parameters
ALLOCATIONS = {
    "community_mining": {
        "pct": 0.50,
        "amount": 500_000_000,
        "label": "Community / Mining",
        "vesting": "30-day unlock (20% immediate + 80% linear)"
    },
    "treasury": {
        "pct": 0.20,
        "amount": 200_000_000,
        "label": "Treasury / Ecosystem",
        "vesting": "Locked until grant approval"
    },
    "team": {
        "pct": 0.10,
        "amount": 100_000_000,
        "label": "Team / Devs (vested)",
        "vesting": "2-year linear vest, 6-month cliff"
    },
    "liquidity": {
        "pct": 0.10,
        "amount": 100_000_000,
        "label": "Liquidity Pool",
        "vesting": "Permanently locked in Raydium"
    },
    "airdrop": {
        "pct": 0.05,
        "amount":  50_000_000,
        "label": "Airdrop",
        "vesting": "7-day lock, then linear unlock"
    },
    "reserve": {
        "pct": 0.05,
        "amount":  50_000_000,
        "label": "Reserve",
        "vesting": "1-year cliff, then linear vest"
    },
}


# ─── Helpers ────────────────────────────────────────────────────────────────

def run(cmd, check=True, capture=True):
    """Run a shell command and return output."""
    print(f"  $ {cmd}")
    result = subprocess.run(
        cmd, shell=True, capture_output=capture,
        text=True, check=check
    )
    if result.stdout:
        print(f"    → {result.stdout.strip()}")
    if result.returncode != 0 and result.stderr:
        print(f"    ✗ {result.stderr.strip()}")
    return result


def check_prerequisites(network):
    """Verify all prerequisites are met."""
    print("\n🔍 Checking prerequisites...")

    # Check Solana CLI
    result = run("solana --version", check=False)
    if result.returncode != 0:
        print("❌ Solana CLI not installed. Run:")
        print("   sh -c \"$(curl -sSfL https://release.solana.com/stable/install)\"")
        sys.exit(1)

    # Check SPL Token CLI
    result = run("spl-token --version", check=False)
    if result.returncode != 0:
        print("❌ SPL Token CLI not installed. Run:")
        print("   cargo install spl-token-cli")
        sys.exit(1)

    # Check keypair
    keypair_path = os.path.expanduser("~/.config/solana/alpha-deploy.json")
    if not os.path.exists(keypair_path):
        print(f"⚠️  Keypair not found at {keypair_path}")
        print("   Creating new keypair...")
        run(f"solana-keygen new --outfile {keypair_path} --no-bip39-passphrase")

    # Check balance
    result = run(f"solana balance --url {network}")
    balance = float(result.stdout.strip().split()[0])
    print(f"   💰 Keypair balance: {balance} SOL")

    if balance < 0.5 and network == "devnet":
        print("   ⚠️  Low balance. Requesting airdrop...")
        run(f"solana airdrop 2 --url {network}")
    elif balance < 2.0 and network == "mainnet-beta":
        print("   ❌ Insufficient SOL for mainnet deployment!")
        print("   You need at least 2 SOL for token creation, metadata, and fees.")
        sys.exit(1)

    return True


def create_token_mint(network, keypair):
    """Create the SPL token mint."""
    print("\n🪙 Creating SPL token mint...")

    cmd = (
        f"spl-token create-token --decimals {TOKEN_CONFIG['decimals']} "
        f"--program-id TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA "
        f"--url {network} "
        f"--with-keypair {keypair}"
    )

    result = run(cmd)
    # Parse the mint address from output
    output = result.stdout + result.stderr
    for line in output.split("\n"):
        if "Creating token" in line or "Mint:" in line:
            parts = line.split()
            for part in parts:
                if len(part) > 30 and part[0].isalnum():
                    return part.strip("()")
    return None


def create_token_account(network, mint, keypair):
    """Create a token account to hold the minted tokens."""
    print("\n📂 Creating token account...")

    result = run(
        f"spl-token create-account {mint} --url {network} --owner {keypair}"
    )

    output = result.stdout + result.stderr
    for line in output.split("\n"):
        if "Creating" in line and "associated token account" in line.lower():
            parts = line.split()
            for part in parts:
                if len(part) > 30 and part[0].isalnum():
                    return part.strip("()")
    return None


def mint_tokens(network, mint, amount, destination=None):
    """Mint tokens to a token account."""
    print(f"\n💰 Minting {amount:,.0f} {TOKEN_CONFIG['symbol']}...")

    cmd = f"spl-token mint {mint} {amount} --url {network}"
    if destination:
        cmd += f" -- {destination}"

    run(cmd)


def set_metadata(network, mint, keypair):
    """Set token metadata (name, symbol, image, etc.)."""
    print("\n🏷️  Setting token metadata...")

    # Create metadata JSON file
    metadata = {
        "name": TOKEN_CONFIG["name"],
        "symbol": TOKEN_CONFIG["symbol"],
        "description": TOKEN_CONFIG["description"],
        "image": TOKEN_CONFIG["image"],
        "extensions": {
            "website": TOKEN_CONFIG["website"],
            "twitter": TOKEN_CONFIG["twitter"],
        },
    }

    metadata_path = "/tmp/alpha-token-metadata.json"
    with open(metadata_path, "w") as f:
        json.dump(metadata, f, indent=2)

    # Upload metadata to Arweave or IPFS (for production, use a real storage)
    # For now, set the URI
    uri = TOKEN_CONFIG["website"] + "/token-metadata.json"

    result = run(
        f"spl-token set-metadata {mint} "
        f"--name \"{TOKEN_CONFIG['name']}\" "
        f"--symbol \"{TOKEN_CONFIG['symbol']}\" "
        f"--url {uri} "
        f"--url {network}"
    )

    return uri


def print_summary(mint, network):
    """Print deployment summary."""
    print("\n" + "=" * 60)
    print("🎉 ALPHA TOKEN DEPLOYED!")
    print("=" * 60)
    print(f"  Network:       {network}")
    print(f"  Token Mint:    {mint}")
    print(f"  Name:          {TOKEN_CONFIG['name']}")
    print(f"  Symbol:        {TOKEN_CONFIG['symbol']}")
    print(f"  Decimals:      {TOKEN_CONFIG['decimals']}")
    print(f"  Total Supply:  {TOKEN_CONFIG['total_supply']:,.0f}")
    print(f"\n  Explorer:      https://solscan.io/token/{mint}" if "mainnet" in network else f"  Explorer:      https://solscan.io/token/{mint}?cluster=devnet")
    print(f"  Website:       {TOKEN_CONFIG['website']}")
    print("=" * 60)
    print("\nNext steps:")
    print("  1. Verify token on Solscan")
    print("  2. Create Raydium liquidity pool")
    print("  3. Update SDK with token mint address")
    print("  4. Begin airdrop distribution")
    print("=" * 60)


# ─── Main ───────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Deploy $ALPHA SPL token on Solana")
    parser.add_argument(
        "--network",
        choices=["devnet", "mainnet-beta"],
        default="devnet",
        help="Solana network (default: devnet)"
    )
    parser.add_argument(
        "--keypair",
        default=None,
        help="Path to keypair file (default: ~/.config/solana/alpha-deploy.json)"
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Show what would be done without executing"
    )
    args = parser.parse_args()

    keypair = args.keypair or os.path.expanduser("~/.config/solana/alpha-deploy.json")
    network = args.network

    print("╔══════════════════════════════════════════════════════════╗")
    print("║       ALPHA NETWORK — SPL Token Deployment              ║")
    print(f"║       Network: {network:<42}║")
    print("╚══════════════════════════════════════════════════════════╝")

    if args.dry_run:
        print("\n📋 Dry run — would execute:")
        print(f"  1. Create SPL token mint ({TOKEN_CONFIG['decimals']} decimals)")
        print(f"  2. Create token account")
        for alloc_name, alloc in ALLOCATIONS.items():
            print(f"  3. Mint {alloc['amount']:,.0f} ALPHA to {alloc['label']}")
        print(f"  4. Set token metadata")
        return

    # Check prerequisites
    check_prerequisites(network)

    # Create token mint
    mint = create_token_mint(network, keypair)
    if not mint:
        print("❌ Failed to create token mint")
        sys.exit(1)

    # Create token account
    token_account = create_token_account(network, mint, keypair)

    # Mint tokens to allocations
    print("\n💰 Distributing token allocations...")
    for alloc_name, alloc in ALLOCATIONS.items():
        print(f"\n  ── {alloc['label']} ({alloc['pct']*100:.0f}%) ──")
        print(f"     Vesting: {alloc['vesting']}")
        mint_tokens(network, mint, alloc["amount"])

    # Set metadata
    set_metadata(network, mint, keypair)

    # Print summary
    print_summary(mint, network)

    # Save deployment info
    deployment_info = {
        "network": network,
        "mint": mint,
        "token_account": token_account,
        "config": TOKEN_CONFIG,
        "allocations": ALLOCATIONS,
    }

    output_path = f"alpha-token-{network}.json"
    with open(output_path, "w") as f:
        json.dump(deployment_info, f, indent=2)
    print(f"\n💾 Deployment info saved to {output_path}")


if __name__ == "__main__":
    main()
