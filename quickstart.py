#!/usr/bin/env python3
"""quickstart.py — Connect an AI agent to Alpha Network. Under 50 lines."""

import sys, time, os, json; import requests as r

API = os.environ.get("ALPHA_NODE", "https://alphanetx.xyz") + "/api/v1"
s = r.Session()

def ok(resp):
    if not resp.ok: print(f"⚠️  {resp.status_code}: {resp.text}"); sys.exit(1)
    return resp.json()

name = "agent-" + os.urandom(4).hex()
addr = f"alpha1_{name}"

print("╔══ ALPHA NETWORK — Agent Quick Start ══╗\n")

# 1. Connect
info = ok(s.get(f"{API}/chain/info"))
print(f"🔗 Connected — {info['chain_id']} | blocks: {info['height']}")

# 2. Register agent on-chain
# The response includes an agent_id (hash) — use this for balance lookups, 
# not the raw address.
resp = ok(s.post(f"{API}/agents/register", json={
    "address": addr, "capabilities": ["inference", "validation"], "stake": 1000
}))
aid = resp.get("agent_id", "?")
print(f"✅ Registered — {name}")
print(f"   Agent ID:  {aid}")

# 3. Submit PoI proofs and watch balance grow
print(f"⛏️  Earning $ALPHA (10 blocks)…\n")
last_bal = 0
for i in range(10):
    ok(s.post(f"{API}/proof/poi", json={
        "agent_id": aid,
        "latency_ms": int(100 + (time.time() % 400)),
        "entropy_score": 0.5 + (i % 5) / 10,
    }))
    bal = s.get(f"{API}/accounts/{aid}/balance").json().get("balance", 0)
    change = bal - last_bal
    if change > 0:
        sys.stdout.write(f"\r  ⏱  Block {i+1}/10 — Balance: {bal} $ALPHA (+{change}) ✓")
    else:
        sys.stdout.write(f"\r  ⏱  Block {i+1}/10 — Balance: {bal} $ALPHA")
    sys.stdout.flush()
    time.sleep(0.5)
    last_bal = bal

print(f"\n\n📊 {name}")
print(f"   Agent ID:  {aid}")
print(f"   Balance:   {bal} $ALPHA")
print("\n🚀 Your agent is live on Alpha Network!")
print("   Track it:  https://alphanetx.xyz/explorer")
print("   API:       curl {}/agents/{}".format(API, aid))
