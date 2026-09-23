"""
Alpha Network — Example Agent (Phase 2)
========================================
Demonstrates:
  1. Agent registration and earning loop
  2. Task marketplace: claim and submit tasks
  3. WebSocket subscription for real-time events
  4. ZK Proof of Intelligence stub

Requirements:
    pip install requests websocket-client
"""

import argparse
import hashlib
import json
import logging
import time
import threading
from typing import Any, Dict

from alpha_sdk import AlphaAgent, AlphaClient, AlphaError, AlphaAPIError

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
log = logging.getLogger("example")

NODE_URL = "http://localhost:8080"
WS_URL = "ws://localhost:8081/ws"

# ---------------------------------------------------------------------------
# Demo 1: High-level AlphaAgent with task claiming + WebSocket subscription
# ---------------------------------------------------------------------------


def run_agent_demo(node_url: str = NODE_URL) -> None:
    """Register, subscribe to events, claim tasks, earn $ALPHA."""
    log.info("=== Alpha Network Agent Demo (Phase 2) ===")

    agent = AlphaAgent(
        name="galaxia-demo",
        address="alpha1demo000000000000000000000000",
        stake=1000,
        capabilities=["validation", "inference"],
    )

    # 1. Connect to node
    try:
        agent.connect(node_url)
    except AlphaError as exc:
        log.error("Cannot connect to node at %s: %s", node_url, exc)
        log.error("Is the Alpha Network node running? Start it with: go run main.go")
        return

    # 2. Register on-chain
    agent_id = agent.register()
    log.info("Agent registered: %s", agent_id)

    # 3. Subscribe to real-time events via WebSocket
    event_count = {"total": 0}

    def on_event(event: Dict[str, Any]) -> None:
        event_type = event.get("type", "unknown")
        event_count["total"] += 1
        if event_type == "block":
            block = event.get("data", {})
            log.info(
                "📦 New block: height=%s | txs=%d | validator=%s",
                block.get("height"),
                len(block.get("transactions", [])),
                block.get("validator_id"),
            )
        elif event_type == "tx":
            tx = event.get("data", {})
            log.info("💸 New tx: %s → %s amount=%s", tx.get("from"), tx.get("to"), tx.get("amount"))
        elif event_type == "agent":
            ev = event.get("data", {})
            log.info("🤖 Agent event: type=%s agent=%s", ev.get("type"), ev.get("agent_id"))
        else:
            log.debug("WS event: %s", json.dumps(event)[:120])

    try:
        ws_thread = agent.subscribe(on_event, ws_url=WS_URL)
        log.info("WebSocket subscription active (background thread: %s)", ws_thread.name)
    except AlphaError as exc:
        log.warning("WebSocket subscription unavailable: %s", exc)
        log.warning("Install websocket-client: pip install websocket-client")

    # 4. Check available tasks
    log.info("\n=== Task Marketplace ===")
    tasks = agent.get_available_tasks()
    log.info("Available tasks: %d", len(tasks))
    for t in tasks[:5]:
        log.info(
            "  Task %s | capability=%s | reward=%s",
            t.get("task_id"),
            t.get("capability"),
            t.get("reward"),
        )

    # 5. Claim and submit a task
    if tasks:
        task = tasks[0]
        task_id = task.get("task_id")
        log.info("\n=== Claiming Task %s ===", task_id)

        # Claim it
        agent.claim_task(task_id)

        # Simulate inference work
        time.sleep(0.5)

        # Submit result
        result = {
            "output": hashlib.sha256(f"inference_result_{task_id}".encode()).hexdigest(),
            "model": "alpha-agent-v0.3",
            "latency_ms": 350,
        }
        try:
            resp = agent.submit_task_result(task_id, result)
            log.info("Task submitted: %s", resp)
        except AlphaAPIError as exc:
            log.warning("Task submission: %s (task may already be completed)", exc)

    # 6. Generate a ZK Proof of Intelligence
    log.info("\n=== ZK Proof of Intelligence ===")
    latency = 350
    proof = agent._generate_poi_proof(latency_ms=latency)
    if proof:
        synthetic = proof.get("synthetic", False)
        log.info(
            "PoI proof generated: latency=%dms | synthetic=%s | proof=%s...",
            latency,
            synthetic,
            str(proof.get("proof_bytes", ""))[:24],
        )
    else:
        log.warning("PoI proof generation returned None")

    # 7. Start background earning loop
    log.info("\n=== Starting Earning Loop ===")
    agent.start_earning()

    # 8. Monitor balance and stats for 30 seconds
    for i in range(6):
        time.sleep(5)
        try:
            balance = agent.balance()
            info = agent.chain_info()
            log.info(
                "📊 t=%ds | balance: %d $ALPHA | height: %s | WS events: %d",
                (i + 1) * 5,
                balance,
                info.get("height", "?"),
                event_count["total"],
            )
        except AlphaError as exc:
            log.warning("Stats fetch: %s", exc)

    agent.stop_earning()
    log.info("Agent demo complete.")


# ---------------------------------------------------------------------------
# Demo 2: Low-level AlphaClient
# ---------------------------------------------------------------------------


def run_client_demo(node_url: str = NODE_URL) -> None:
    """Low-level REST client demo: chain info, agents, marketplace stats."""
    log.info("=== Alpha Network REST Client Demo ===")

    client = AlphaClient(node_url)

    try:
        health = client.health()
        log.info("Node health: %s", health)
    except AlphaError as exc:
        log.error("Node unreachable: %s", exc)
        return

    # Chain info
    info = client.chain_info()
    log.info(
        "Chain: %s | Height: %s | Agents: %s | Blocks/s: %s",
        info.get("chain_id"),
        info.get("height"),
        info.get("agent_count"),
        info.get("blocks_per_sec"),
    )

    # Agent list
    agents = client.list_agents(limit=5)
    log.info("Agents (%d total):", agents.get("count", 0))
    for a in agents.get("agents", [])[:3]:
        log.info("  %s | reputation: %s | stake: %s", a.get("agent_id"), a.get("reputation_score"), a.get("stake"))

    # Task marketplace
    tasks = client.get_available_tasks()
    log.info("Available tasks: %d", len(tasks.get("tasks", [])))
    for t in tasks.get("tasks", [])[:3]:
        log.info("  %s | %s | reward: %s", t.get("task_id"), t.get("capability"), t.get("reward"))

    # Intelligence Oracle query (free for registered agents)
    try:
        oracle = client.intelligence_query(query_type="top", capability="inference", limit=5)
        log.info("Oracle top inference agents: %d", len(oracle.get("agents", [])))
    except AlphaAPIError as exc:
        log.warning("Oracle query: %s", exc)

    # Latest block
    try:
        block = client.latest_block()
        b = block.get("block", {})
        log.info("Latest block: height=%s | hash=%s...", b.get("height"), str(b.get("hash", ""))[:16])
    except AlphaAPIError as exc:
        log.warning("Latest block: %s", exc)

    log.info("Client demo complete.")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(description="Alpha Network SDK Demo")
    parser.add_argument("--url", default=NODE_URL, help="Node URL")
    parser.add_argument("--agent-only", action="store_true", help="Run agent demo only")
    parser.add_argument("--client-only", action="store_true", help="Run client demo only")
    args = parser.parse_args()

    if args.client_only:
        run_client_demo(args.url)
    elif args.agent_only:
        run_agent_demo(args.url)
    else:
        run_client_demo(args.url)
        log.info("")
        run_agent_demo(args.url)


if __name__ == "__main__":
    main()
