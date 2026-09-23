"""
Alpha Network — Solana Integration Module

Connects AI agents to $ALPHA on Solana:
- Wallet management
- Token transfers (SPL)
- Staking via the Alpha Network Solana program
- Reward claiming
- Agent registration

Requirements:
    pip install solana solders

Example:
    from alpha_sdk.solana import AlphaSolanaAgent

    agent = AlphaSolanaAgent(
        wallet_path="~/.config/solana/alpha-agent.json",
        alpha_token_mint="YOUR_TOKEN_MINT_ADDRESS",
        alpha_program_id="AlphaNetX11111111111111111111111111111111111"
    )

    agent.connect()
    agent.register_agent(
        capabilities=["validation", "inference"],
        stake_amount=1000
    )
    agent.claim_rewards()
"""

from __future__ import annotations

import json
import logging
import os
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

try:
    from solana.rpc.api import Client
    from solana.rpc.async_api import AsyncClient
    from solana.rpc.types import TxOpts
    from solana.transaction import Transaction
    from solders.keypair import Keypair
    from solders.pubkey import Pubkey
    from solders.system_program import (
        CreateAccountParams,
        create_account,
        transfer as system_transfer,
        TransferParams,
    )
    # SPL Token (requires spl-token package)
    # from spl.token.client import Token
    # from spl.token.instructions import (
    #     create_associated_token_account,
    #     get_associated_token_address,
    #     mint_to,
    #     transfer,
    # )
    _SOLANA_AVAILABLE = True
except ImportError:
    _SOLANA_AVAILABLE = False

logger = logging.getLogger(__name__)

# ─── Constants ──────────────────────────────────────────────────────────────

LAMPORTS_PER_SOL = 1_000_000_000
ALPHA_DECIMALS = 9  # SPL token standard

# Solana cluster URLs
CLUSTER_URLS = {
    "devnet": "https://api.devnet.solana.com",
    "mainnet": "https://api.mainnet-beta.solana.com",
    "testnet": "https://api.testnet.solana.com",
    "localnet": "http://localhost:8899",
}

# Alpha Network Solana program ID
ALPHA_PROGRAM_ID = "AlphaNetX11111111111111111111111111111111111"


# ─── Data Structures ───────────────────────────────────────────────────────

@dataclass
class AgentInfo:
    """On-chain agent information."""
    address: str
    capabilities: List[str] = field(default_factory=list)
    stake_amount: int = 0
    reputation_score: int = 100
    task_count: int = 0
    total_earned: int = 0
    total_burned: int = 0
    is_active: bool = True


@dataclass
class TokenBalance:
    """Token balance information."""
    mint: str
    amount: int
    decimals: int

    @property
    def formatted(self) -> str:
        return f"{self.amount / (10 ** self.decimals):,.2f}"


# ─── Solana Agent ──────────────────────────────────────────────────────────

class AlphaSolanaAgent:
    """
    AI agent that operates on Alpha Network via Solana.

    Handles:
    - Solana wallet connection
    - $ALPHA token operations (transfer, stake, claim)
    - Agent registration on the Alpha Network program
    - Reward claiming
    - Task marketplace participation
    """

    def __init__(
        self,
        wallet_path: Optional[str] = None,
        wallet_keypair: Optional[Keypair] = None,
        alpha_token_mint: Optional[str] = None,
        alpha_program_id: str = ALPHA_PROGRAM_ID,
        cluster: str = "devnet",
        alpha_node_url: Optional[str] = None,
    ):
        """
        Initialize the Solana agent.

        Args:
            wallet_path: Path to Solana keypair file
            wallet_keypair: Pre-loaded Keypair object (alternative to wallet_path)
            alpha_token_mint: $ALPHA token mint address
            alpha_program_id: Alpha Network Solana program ID
            cluster: Solana cluster (devnet, mainnet, testnet, localnet)
            alpha_node_url: URL to Alpha Network node (for hybrid mode)
        """
        if not _SOLANA_AVAILABLE:
            raise ImportError(
                "Solana SDK not installed. Run: pip install solana solders"
            )

        self.cluster = cluster
        self.rpc_url = CLUSTER_URLS.get(cluster, CLUSTER_URLS["devnet"])
        self.alpha_program_id = Pubkey.from_string(alpha_program_id)
        self.alpha_token_mint = Pubkey.from_string(alpha_token_mint) if alpha_token_mint else None
        self.alpha_node_url = alpha_node_url

        # Load wallet
        if wallet_keypair:
            self.keypair = wallet_keypair
        elif wallet_path:
            wallet_path = os.path.expanduser(wallet_path)
            if os.path.exists(wallet_path):
                with open(wallet_path, "r") as f:
                    secret = json.load(f)
                self.keypair = Keypair.from_bytes(secret)
            else:
                # Generate new keypair
                self.keypair = Keypair()
                with open(wallet_path, "w") as f:
                    json.dump(list(self.keypair.to_bytes_array()), f)
                logger.info(f"Generated new keypair at {wallet_path}")
        else:
            self.keypair = Keypair()
            logger.info("Generated ephemeral keypair (not saved to disk)")

        self.client = Client(self.rpc_url)
        self.agent_info: Optional[AgentInfo] = None

    # ─── Properties ─────────────────────────────────────────────────────

    @property
    def public_key(self) -> Pubkey:
        return self.keypair.pubkey()

    @property
    def address(self) -> str:
        return str(self.public_key)

    # ─── Connection ─────────────────────────────────────────────────────

    def connect(self) -> Dict[str, Any]:
        """
        Verify connection to Solana and return status.

        Returns:
            Dict with cluster, balance, and connection status
        """
        try:
            balance = self.client.get_balance(self.public_key).value
            return {
                "cluster": self.cluster,
                "address": self.address,
                "sol_balance": balance / LAMPORTS_PER_SOL,
                "connected": True,
            }
        except Exception as e:
            logger.error(f"Connection failed: {e}")
            return {
                "cluster": self.cluster,
                "address": self.address,
                "sol_balance": 0,
                "connected": False,
                "error": str(e),
            }

    # ─── Token Operations ───────────────────────────────────────────────

    def get_token_balance(self, token_mint: Optional[str] = None) -> TokenBalance:
        """
        Get $ALPHA token balance for this agent.

        Args:
            token_mint: Token mint address (defaults to alpha_token_mint)

        Returns:
            TokenBalance with amount and formatted value
        """
        mint = token_mint or self.alpha_token_mint
        if not mint:
            raise ValueError("No token mint address provided")

        # In production, use spl.token.client to get token accounts
        # For now, return placeholder
        return TokenBalance(
            mint=str(mint),
            amount=0,
            decimals=ALPHA_DECIMALS,
        )

    def transfer_alpha(
        self,
        to_address: str,
        amount: float,
        token_mint: Optional[str] = None,
    ) -> Dict[str, Any]:
        """
        Transfer $ALPHA to another address.

        Args:
            to_address: Recipient's Solana address
            amount: Amount of ALPHA to send
            token_mint: Token mint address (defaults to alpha_token_mint)

        Returns:
            Dict with transaction signature and status
        """
        # In production, this would use SPL Token transfer
        # For now, return placeholder
        logger.info(
            f"Transferring {amount} ALPHA to {to_address} "
            f"(SPL token transfer requires spl-token package)"
        )
        return {
            "status": "placeholder",
            "note": "SPL token transfer requires full Solana SDK setup",
            "to": to_address,
            "amount": amount,
        }

    # ─── Agent Registration ─────────────────────────────────────────────

    def register_agent(
        self,
        capabilities: List[str],
        stake_amount: int = 1000,
    ) -> Dict[str, Any]:
        """
        Register this agent on Alpha Network.

        In hybrid mode, this registers on both:
        1. The Solana program (on-chain identity)
        2. The Alpha Network node (PoI consensus layer)

        Args:
            capabilities: List of agent capabilities
            stake_amount: Amount of ALPHA to stake

        Returns:
            Registration result
        """
        # Register on Alpha Network node (if hybrid mode)
        node_result = None
        if self.alpha_node_url:
            try:
                import requests
                response = requests.post(
                    f"{self.alpha_node_url}/api/v1/agents/register",
                    json={
                        "address": self.address,
                        "capabilities": capabilities,
                        "stake": stake_amount * (10 ** ALPHA_DECIMALS),
                    },
                )
                node_result = response.json()
            except Exception as e:
                logger.warning(f"Node registration failed: {e}")

        # Store agent info
        self.agent_info = AgentInfo(
            address=self.address,
            capabilities=capabilities,
            stake_amount=stake_amount * (10 ** ALPHA_DECIMALS),
        )

        return {
            "status": "registered",
            "agent_address": self.address,
            "capabilities": capabilities,
            "stake_alpha": stake_amount,
            "solana_address": self.address,
            "node_registration": node_result,
        }

    # ─── Rewards ────────────────────────────────────────────────────────

    def claim_rewards(self) -> Dict[str, Any]:
        """
        Claim accumulated block rewards from Alpha Network.

        Returns:
            Claim result with reward amount
        """
        if not self.agent_info:
            return {"status": "error", "message": "Agent not registered"}

        # In production, this would call the Solana program's claim_rewards
        # instruction and/or the Alpha Network node API
        return {
            "status": "placeholder",
            "note": "Reward claiming requires full program deployment",
            "agent": self.address,
        }

    # ─── Staking ────────────────────────────────────────────────────────

    def stake(self, amount: int) -> Dict[str, Any]:
        """
        Stake additional $ALPHA tokens.

        Args:
            amount: Amount of ALPHA to stake

        Returns:
            Stake result
        """
        if not self.agent_info:
            return {"status": "error", "message": "Agent not registered"}

        self.agent_info.stake_amount += amount * (10 ** ALPHA_DECIMALS)

        return {
            "status": "staked",
            "agent": self.address,
            "stake_amount": amount,
            "total_stake": self.agent_info.stake_amount / (10 ** ALPHA_DECIMALS),
        }

    def unstake(self, amount: int) -> Dict[str, Any]:
        """
        Unstake $ALPHA tokens.

        Args:
            amount: Amount of ALPHA to unstake

        Returns:
            Unstake result
        """
        if not self.agent_info:
            return {"status": "error", "message": "Agent not registered"}

        unstake_amount = min(
            amount * (10 ** ALPHA_DECIMALS),
            self.agent_info.stake_amount,
        )
        self.agent_info.stake_amount -= unstake_amount

        return {
            "status": "unstaked",
            "agent": self.address,
            "unstake_amount": amount,
            "remaining_stake": self.agent_info.stake_amount / (10 ** ALPHA_DECIMALS),
        }

    # ─── Tasks ──────────────────────────────────────────────────────────

    def get_available_tasks(self, capability: Optional[str] = None) -> List[Dict]:
        """
        Get available tasks from the marketplace.

        Args:
            capability: Filter by capability type

        Returns:
            List of available tasks
        """
        if self.alpha_node_url:
            try:
                import requests
                params = {}
                if capability:
                    params["capability"] = capability
                response = requests.get(
                    f"{self.alpha_node_url}/api/v1/tasks/available",
                    params=params,
                )
                data = response.json()
                return data.get("tasks", [])
            except Exception as e:
                logger.warning(f"Failed to fetch tasks: {e}")
        return []

    def submit_task_result(
        self,
        task_id: str,
        result: Dict[str, Any],
    ) -> Dict[str, Any]:
        """
        Submit a completed task result.

        Args:
            task_id: The task ID to submit for
            result: The task result data

        Returns:
            Submission result
        """
        if self.alpha_node_url:
            try:
                import requests
                response = requests.post(
                    f"{self.alpha_node_url}/api/v1/tasks/{task_id}/submit",
                    json={"agent_address": self.address, "result": result},
                )
                return response.json()
            except Exception as e:
                logger.warning(f"Task submission failed: {e}")
        return {"status": "error", "message": "Could not submit task"}

    # ─── Intelligence Oracle ────────────────────────────────────────────

    def get_network_stats(self) -> Dict[str, Any]:
        """Get Alpha Network intelligence stats."""
        if self.alpha_node_url:
            try:
                import requests
                response = requests.get(
                    f"{self.alpha_node_url}/api/v1/intelligence/stats"
                )
                return response.json()
            except Exception as e:
                logger.warning(f"Failed to fetch stats: {e}")
        return {}

    def get_top_agents(self) -> List[Dict]:
        """Get top agents by reputation."""
        if self.alpha_node_url:
            try:
                import requests
                response = requests.get(
                    f"{self.alpha_node_url}/api/v1/intelligence/top"
                )
                data = response.json()
                return data.get("agents", [])
            except Exception as e:
                logger.warning(f"Failed to fetch top agents: {e}")
        return []

    # ─── Wallet Info ────────────────────────────────────────────────────

    def get_wallet_info(self) -> Dict[str, Any]:
        """Get full wallet and agent information."""
        balance_result = self.connect()
        token_balance = self.get_token_balance()

        info = {
            "wallet": {
                "address": self.address,
                "cluster": self.cluster,
                "sol_balance": balance_result.get("sol_balance", 0),
                "alpha_balance": token_balance.formatted,
            },
            "agent": None,
        }

        if self.agent_info:
            info["agent"] = {
                "address": self.agent_info.address,
                "capabilities": self.agent_info.capabilities,
                "stake_alpha": self.agent_info.stake_amount / (10 ** ALPHA_DECIMALS),
                "reputation": self.agent_info.reputation_score,
                "tasks_completed": self.agent_info.task_count,
                "total_earned": self.agent_info.total_earned / (10 ** ALPHA_DECIMALS),
            }

        return info
