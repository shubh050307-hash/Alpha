#!/usr/bin/env python3
"""
Alpha Network Discord Bot — automated announcements for #general.

Features:
  1. ⏳ Presale countdown every 12 hours
  2. 🧱 Block milestone alerts every 100,000 blocks
  3. 📊 Daily network stats at 12:00 UTC

Dependencies: requests (pip install requests)
Env vars:   DISCORD_BOT_TOKEN, DISCORD_GENERAL_CHANNEL_ID
"""

import json
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

import requests

# ── Config ────────────────────────────────────────────────────────────────────

DISCORD_TOKEN = os.environ["DISCORD_BOT_TOKEN"]
CHANNEL_ID = os.environ["DISCORD_GENERAL_CHANNEL_ID"]  # #general channel

HEALTH_URL = "http://localhost:8080/health"
CHAIN_URL = "http://localhost:8080/api/v1/chain/info"
DISCORD_API = "https://discord.com/api/v10"

PRESALE_END = datetime(2026, 5, 30, 12, 0, 0, tzinfo=timezone.utc)
MILESTONE_INTERVAL = 100_000
STATE_FILE = Path(__file__).parent / "milestone-state.json"

HEADERS = {
    "Authorization": f"Bot {DISCORD_TOKEN}",
    "Content-Type": "application/json",
}

# ── State ─────────────────────────────────────────────────────────────────────

def load_state():
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text())
    return {"last_milestone": 0, "last_daily": "", "last_countdown": ""}

def save_state(s):
    STATE_FILE.write_text(json.dumps(s, indent=2))

# ── Discord ───────────────────────────────────────────────────────────────────

def send_message(content: str) -> bool:
    url = f"{DISCORD_API}/channels/{CHANNEL_ID}/messages"
    try:
        r = requests.post(url, json={"content": content}, headers=HEADERS, timeout=10)
        if r.status_code == 200:
            return True
        print(f"[discord] HTTP {r.status_code}: {r.text[:200]}", file=sys.stderr)
    except Exception as e:
        print(f"[discord] {e}", file=sys.stderr)
    return False

# ── Task 1: Presale Countdown ─────────────────────────────────────────────────

def presale_countdown(state: dict):
    """Post countdown every 12 hours while presale is active."""
    now = datetime.now(timezone.utc)
    remaining = PRESALE_END - now
    if remaining.total_seconds() <= 0:
        return  # presale ended

    if state.get("last_countdown"):
        try:
            last = datetime.fromisoformat(state["last_countdown"])
            if (now - last).total_seconds() < 12 * 3600:
                return
        except (ValueError, TypeError):
            pass  # corrupted timestamp, re-post

    days, secs = remaining.days, remaining.seconds
    hours = secs // 3600

    # Save state BEFORE posting — prevents duplicate posts if Discord fails
    state["last_countdown"] = now.isoformat()
    save_state(state)

    msg = (
        f"⏳ **$ALPHA Presale Countdown**\n\n"
        f"**{days} days {hours} hours remaining**\n"
        f"🕐 Closes <t:{int(PRESALE_END.timestamp())}:R>\n\n"
        f"💰 **50,000 $ALPHA per SOL**\n"
        f"📌 Min contribution: **0.12 SOL**\n"
        f"🎁 Use code **PRODUCTHUNT** for +10,000 $ALPHA bonus\n\n"
        f"🔗 https://alphanetx.xyz/presale"
    )

    if send_message(msg):
        print(f"[presale] Posted countdown — {days}d {hours}h remaining")
    else:
        print(f"[presale] Discord send failed (state already saved)", file=sys.stderr)

# ── Task 2: Block Milestones ─────────────────────────────────────────────────

def block_milestones(state: dict):
    """Post when the chain crosses a new 100,000-block milestone."""
    try:
        r = requests.get(HEALTH_URL, timeout=5)
        r.raise_for_status()
        height = r.json()["height"]
    except Exception as e:
        print(f"[milestone] health fetch failed: {e}", file=sys.stderr)
        return

    last = state.get("last_milestone", 0)

    # On first run, seed from current height
    if last == 0:
        last = (height // MILESTONE_INTERVAL) * MILESTONE_INTERVAL
        state["last_milestone"] = last
        save_state(state)
        print(f"[milestone] Seeded at {last:,} (current height {height:,})")
        return

    next_ms = ((last // MILESTONE_INTERVAL) + 1) * MILESTONE_INTERVAL
    if height < next_ms:
        return

    # Save state BEFORE posting — prevents duplicate posts if Discord fails
    state["last_milestone"] = next_ms
    save_state(state)

    msg = (
        f"🧱 **Block Milestone!**\n\n"
        f"Alpha Network just crossed **{next_ms:,} blocks** 🎉\n"
        f"Current height: **{height:,}**\n"
        f"Chain: `alpha-1` | Consensus: Proof of Intelligence\n\n"
        f"🔗 https://alphanetx.xyz"
    )

    if send_message(msg):
        print(f"[milestone] Posted {next_ms:,} blocks")
    else:
        print(f"[milestone] Discord send failed (state already saved)", file=sys.stderr)

# ── Task 3: Daily Stats ──────────────────────────────────────────────────────

def daily_stats(state: dict):
    """Post network stats every day at 12:00 UTC."""
    now = datetime.now(timezone.utc)
    if now.hour != 12:
        return

    today = now.strftime("%Y-%m-%d")
    if state.get("last_daily") == today:
        return

    try:
        r = requests.get(CHAIN_URL, timeout=5)
        r.raise_for_status()
        c = r.json()
    except Exception as e:
        print(f"[daily] chain fetch failed: {e}", file=sys.stderr)
        return

    height = c.get("height", 0)
    agents = c.get("agent_count", 0)
    supply = c.get("total_supply", 0)
    burned = c.get("total_burned", 0)
    bps = c.get("blocks_per_sec", 0)
    uptime = c.get("uptime", "N/A")

    # Save state BEFORE posting — prevents duplicate posts if Discord rate-limits us
    state["last_daily"] = today
    save_state(state)

    msg = (
        f"📊 **Alpha Network Daily Stats** — {today}\n\n"
        f"🧱 Height: **{height:,}**\n"
        f"⚡ Speed: **{bps:.1f} bps**\n"
        f"🤖 Agents: **{agents}**\n"
        f"💰 Supply: **{supply:,} $ALPHA**\n"
        f"🔥 Burned: **{burned:,} $ALPHA**\n"
        f"⏱️ Uptime: **{uptime}**\n"
        f"🌐 `alpha-1` testnet\n\n"
        f"🔗 https://alphanetx.xyz"
    )

    if send_message(msg):
        print(f"[daily] Posted stats for {today}")
    else:
        print(f"[daily] Discord send failed (state already saved for {today})", file=sys.stderr)

# ── Main Loop ─────────────────────────────────────────────────────────────────

def main():
    print("[alpha-bot] Starting Alpha Network Discord Bot", flush=True)
    state = load_state()
    print(f"[alpha-bot] Milestone tracker set at {state['last_milestone']:,}", flush=True)

    while True:
        try:
            presale_countdown(state)
            time.sleep(2)  # ⸻ avoid Discord rate limiting

            block_milestones(state)
            time.sleep(2)

            daily_stats(state)
        except Exception as e:
            print(f"[alpha-bot] loop error: {e}", file=sys.stderr)

        time.sleep(56)  # total loop ~60s


if __name__ == "__main__":
    main()
