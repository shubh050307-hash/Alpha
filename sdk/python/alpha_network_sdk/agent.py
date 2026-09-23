"""
Alpha Network Agent — High-level agent with model presets and challenge mode.

Provides:
  - MODEL_PRESETS: template, inference, validator, trader
  - create_agent(): factory for model-based agent creation
  - run_challenge_mode(): task-challenge automation

Re-exports AlphaAgent/AlphaClient from alpha_sdk for convenience.
"""

from __future__ import annotations

import hashlib
import json
import sys
import time
import uuid
from typing import Any, Dict, List, Optional

from .alpha_sdk import (
    AlphaAgent,
    AlphaClient,
    AlphaAPIError,
    AlphaConnectionError,
    BehavioralFingerprint,
    __version__,
)

__all__ = [
    "AlphaAgent",
    "AlphaClient",
    "AlphaAPIError",
    "AlphaConnectionError",
    "BehavioralFingerprint",
    "MODEL_PRESETS",
    "create_agent",
    "run_challenge_mode",
    "__version__",
]

# ── Model Presets ──────────────────────────────────────────────────────────────

MODEL_PRESETS: Dict[str, Dict[str, Any]] = {
    "template": {
        "capabilities": ["validation"],
        "stake": 1000,
        "name_prefix": "template-agent",
        "description": "Minimal template agent — validation only, lowest stake",
    },
    "inference": {
        "capabilities": ["inference", "validation"],
        "stake": 5000,
        "name_prefix": "inference-agent",
        "description": "Inference-capable agent — can complete AI inference tasks",
    },
    "validator": {
        "capabilities": ["validation"],
        "stake": 10000,
        "name_prefix": "validator-agent",
        "description": "Validator agent — high-stake node security and consensus validation",
    },
    "trader": {
        "capabilities": ["analysis", "validation"],
        "stake": 5000,
        "name_prefix": "trader-agent",
        "description": "Analysis agent — market and intelligence data processing",
    },
}


def _generate_address(prefix: str = "alpha1") -> str:
    """Generate a deterministic agent address from a random seed."""
    seed = f"{prefix}:{uuid.uuid4().hex}:{time.time_ns()}"
    h = hashlib.sha256(seed.encode()).hexdigest()
    return f"{prefix}{h[:38]}"


# ── Factory ────────────────────────────────────────────────────────────────────


def create_agent(
    model: str = "template",
    address: Optional[str] = None,
    node_url: str = "http://localhost:8080",
    register: bool = True,
) -> AlphaAgent:
    """
    Create and optionally connect/register an agent using a model preset.

    Args:
        model: One of 'template', 'inference', 'validator', 'trader'
        address: On-chain address (auto-generated if None)
        node_url: Alpha Network node URL
        register: Whether to register immediately
    """
    preset = MODEL_PRESETS.get(model)
    if preset is None:
        valid = ", ".join(MODEL_PRESETS)
        raise ValueError(f"Unknown model '{model}'. Choose from: {valid}")

    if address is None:
        address = _generate_address()

    name = f"{preset['name_prefix']}-{uuid.uuid4().hex[:8]}"

    agent = AlphaAgent(
        name=name,
        address=address,
        stake=preset["stake"],
        capabilities=preset["capabilities"],
    )

    print(f"⚡ Created {model} agent: {name}")
    print(f"   Address:      {address}")
    print(f"   Capabilities: {preset['capabilities']}")
    print(f"   Stake:        {preset['stake']} $ALPHA")

    agent.connect(node_url)

    if register:
        try:
            agent_id = agent.register()
            print(f"   Agent ID:     {agent_id}")
        except Exception as exc:
            print(f"   ⚠️  Registration skipped: {exc}")

    return agent


# ── Challenge Mode ─────────────────────────────────────────────────────────────


def run_challenge_mode(
    node_url: str = "http://localhost:8080",
    capability: Optional[str] = None,
    max_challenges: int = 5,
) -> None:
    """
    Connect to the node and list available challenges (tasks) from the marketplace.

    Prints a table of available tasks/challenges for agents to pick up.
    """
    client = AlphaClient(node_url)

    try:
        info = client.chain_info()
        print(f"⚡ Alpha Network Challenge Board")
        print(f"   Chain:    {info.get('chain_id', '?')}")
        print(f"   Height:   {info.get('height', '?')}")
        print(f"   Uptime:   {info.get('uptime_seconds', '?')}s")
        print()
    except AlphaConnectionError:
        print(f"❌ Cannot reach node at {node_url}")
        sys.exit(1)

    try:
        tasks = client.get_available_tasks(capability=capability)
        task_list = tasks.get("tasks", [])[:max_challenges]
    except AlphaAPIError:
        # Fall back to regular task list
        try:
            tasks = client.list_tasks()
            task_list = tasks.get("tasks", [])[:max_challenges]
        except AlphaAPIError as exc:
            print(f"⚠️  Task marketplace unavailable: {exc}")
            task_list = []

    if not task_list:
        print("   No challenges available right now.")
        print("   Tip: Post a task with POST /api/v1/tasks/post")
        return

    print(f"   {'Task ID':<14} {'Capability':<14} {'Reward':<10} {'Status':<10}")
    print(f"   {'─'*14} {'─'*14} {'─'*10} {'─'*10}")
    for t in task_list:
        tid = str(t.get("task_id", "?"))[:13]
        cap = str(t.get("capability", "?"))[:13]
        reward = str(t.get("reward", "?"))[:9]
        status = str(t.get("status", "?"))[:9]
        print(f"   {tid:<14} {cap:<14} {reward:<10} {status:<10}")

    print(f"\n   {len(task_list)} challenge(s) shown (limit: {max_challenges})")
    print(f"   Use: alpha-agent start --model inference  to start earning")
