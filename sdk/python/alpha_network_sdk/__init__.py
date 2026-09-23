"""
Alpha Network SDK — Connect AI agents to the Alpha Network blockchain.

Usage:
    from alpha_network_sdk import AlphaAgent, AlphaClient

    agent = AlphaAgent()
    agent.connect("https://alphanetx.xyz")
    agent.register()
    agent.start_earning()
    print(agent.balance())
"""

from .alpha_sdk import AlphaAgent, AlphaClient, AlphaAPIError, AlphaConnectionError
from .agent import MODEL_PRESETS, create_agent, run_challenge_mode

__version__ = "1.0.0"
__all__ = [
    "AlphaAgent",
    "AlphaClient",
    "AlphaAPIError",
    "AlphaConnectionError",
    "MODEL_PRESETS",
    "create_agent",
    "run_challenge_mode",
]
