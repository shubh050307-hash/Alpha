#!/usr/bin/env python3
"""
Alpha Network — Foreign Agent Detector
Checks the intelligence leaderboard for addresses not matching known test agents.
Logs discoveries and optionally fires a Discord webhook.
"""
import json, os, sys, urllib.request, datetime

LEADERBOARD_URL = "https://alphanetx.xyz/api/v1/intelligence/leaderboard?limit=50"
LOG_PATH = os.path.expanduser("/var/log/alpha-new-agents.log")

# Patterns that must appear somewhere in the address to be considered known/test
KNOWN_PATTERNS = [
    "testautonomous", "testtask",
    "scientific", "economic", "systems",
    "voter", "oracle",
    "challenge_", "autoagent",
]

def fetch_leaderboard():
    req = urllib.request.Request(LEADERBOARD_URL)
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())

def detect_new(agents):
    unknown = []
    for ag in agents:
        addr = ag["address"]
        if not any(p in addr for p in KNOWN_PATTERNS):
            unknown.append(ag)
    return unknown

def log_discovery(agents):
    ts = datetime.datetime.utcnow().isoformat()
    with open(LOG_PATH, "a") as f:
        for ag in agents:
            line = f"[{ts}] NEW {ag['address']} | tier={ag['trust_tier']} | iq={ag['iq_score']} | last_active={ag['last_active']}\n"
            f.write(line)
            print(line, end="")

def notify_discord(agents):
    webhook = os.environ.get("DISCORD_WEBHOOK_URL")
    if not webhook:
        return
    lines = []
    for ag in agents:
        lines.append(f"🆕 **{ag['address']}**\n"
                     f"> Tier: {ag['trust_tier']} | IQ: {ag['iq_score']} | "
                     f"Last active: {ag['last_active'][:19]}")
    body = {
        "content": "**⚠️ Unfamiliar agent(s) detected on Alpha Network:**\n\n" + "\n\n".join(lines)
    }
    data = json.dumps(body).encode()
    req = urllib.request.Request(webhook, data=data, headers={"Content-Type": "application/json"})
    try:
        urllib.request.urlopen(req, timeout=10)
    except Exception as e:
        print(f"[warn] Discord webhook failed: {e}", file=sys.stderr)

def main():
    try:
        data = fetch_leaderboard()
        agents = data["data"]["leaderboard"]
        unknown = detect_new(agents)
        if unknown:
            log_discovery(unknown)
            notify_discord(unknown)
            print(f"→ {len(unknown)} unfamiliar agent(s) logged")
            sys.exit(0)
        else:
            print("→ All clear — no unfamiliar agents")
    except Exception as e:
        print(f"[error] {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
