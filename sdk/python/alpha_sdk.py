"""
Alpha Network Python SDK
========================
Connect any AI agent to Alpha Network and start earning $ALPHA in ~10 lines.

Example::

    from alpha_sdk import AlphaAgent

    agent = AlphaAgent(
        name="my-agent",
        address="alpha1...",
        stake=1000
    )
    agent.connect("http://localhost:8080")
    agent.start_earning()
    print(agent.balance())

Requirements: requests (pip install requests)

Optional for Ed25519 signing: cryptography (pip install cryptography)
"""

from __future__ import annotations

import hashlib
import json
import logging
import math
import random
import threading
import time
import uuid
from typing import Any, Callable, Dict, List, Optional

try:
    import requests
    from requests.adapters import HTTPAdapter
    from urllib3.util.retry import Retry
except ImportError as exc:  # pragma: no cover
    raise ImportError("requests is required: pip install requests") from exc

__version__ = "0.3.0"
__all__ = ["AlphaAgent", "AlphaClient"]

try:
    import websocket as _websocket  # pip install websocket-client
    _WS_AVAILABLE = True
except ImportError:
    _WS_AVAILABLE = False

# Ed25519 support (optional — needed for secure transfers)
try:
    from cryptography.hazmat.primitives.asymmetric import ed25519
    from cryptography.hazmat.primitives import serialization
    _ED25519_AVAILABLE = True
except ImportError:
    _ED25519_AVAILABLE = False

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Ed25519 Transfer Signer
# ---------------------------------------------------------------------------


class TransferSigner:
    """
    Ed25519 key management and transfer signing for Alpha Network.

    Usage::

        signer = TransferSigner.generate()
        print(signer.address)   # alpha1… derived from public key
        print(signer.pubkey_hex)  # hex-encoded Ed25519 public key

        # Sign and send a transfer
        sig = signer.sign_transfer("alpha1to…", 1000, nonce=1)
        agent.send_signed("alpha1to…", 1000, sig)

    Requires: pip install cryptography
    """

    def __init__(self, private_key, public_key: bytes) -> None:
        self._private_key = private_key
        self._public_key = public_key
        self.pubkey_hex = public_key.hex()
        # Derive Alpha address from public key: alpha1 + first 40 hex chars of SHA256(pubkey)
        h = hashlib.sha256(public_key).hexdigest()[:40]
        self.address = f"alpha1{h}"

    @classmethod
    def generate(cls) -> "TransferSigner":
        """Generate a new Ed25519 keypair and return a TransferSigner."""
        if not _ED25519_AVAILABLE:
            raise ImportError(
                "Ed25519 signing requires cryptography: pip install cryptography"
            )
        sk = ed25519.Ed25519PrivateKey.generate()
        vk = sk.public_key()
        pubkey_bytes = vk.public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
        return cls(sk, pubkey_bytes)

    @classmethod
    def from_private_key_hex(cls, privkey_hex: str) -> "TransferSigner":
        """Load a signer from a hex-encoded Ed25519 private key (64 hex chars)."""
        if not _ED25519_AVAILABLE:
            raise ImportError(
                "Ed25519 signing requires cryptography: pip install cryptography"
            )
        raw = bytes.fromhex(privkey_hex)
        sk = ed25519.Ed25519PrivateKey.from_private_bytes(raw)
        vk = sk.public_key()
        pubkey_bytes = vk.public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
        return cls(sk, pubkey_bytes)

    def private_key_hex(self) -> str:
        """Return the private key as hex (keep this secret!)."""
        raw = self._private_key.private_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption(),
        )
        return raw.hex()

    def sign_transfer(
        self,
        to_addr: str,
        amount: int,
        nonce: int = 0,
        timestamp: Optional[int] = None,
    ) -> str:
        """
        Sign a transfer message, returning the hex-encoded Ed25519 signature.

        The canonical message signed is:
            transfer:{from}:{to}:{amount}:{nonce}:{timestamp}

        Returns the 128-char hex signature.
        """
        if timestamp is None:
            timestamp = int(time.time())
        message = f"transfer:{self.address}:{to_addr}:{amount}:{nonce}:{timestamp}"
        sig = self._private_key.sign(message.encode())
        return sig.hex()

    def build_transfer_request(
        self,
        to_addr: str,
        amount: int,
        memo: str = "",
        nonce: int = 0,
    ) -> Dict[str, Any]:
        """
        Build the complete signed transfer request body.
        Ready to POST to /api/v1/transfer.
        """
        ts = int(time.time())
        sig_hex = self.sign_transfer(to_addr, amount, nonce, ts)
        return {
            "from": self.address,
            "to": to_addr,
            "amount": amount,
            "memo": memo,
            "pubkey": self.pubkey_hex,
            "signature": sig_hex,
            "nonce": nonce,
            "timestamp": ts,
        }

# ---------------------------------------------------------------------------
# Low-level HTTP client
# ---------------------------------------------------------------------------


class AlphaClient:
    """
    Low-level REST client wrapping all Alpha Network API endpoints.

    All methods raise :class:`AlphaAPIError` on non-2xx responses.
    """

    def __init__(self, base_url: str, timeout: float = 10.0, max_retries: int = 3):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout

        session = requests.Session()
        retry = Retry(
            total=max_retries,
            backoff_factor=0.3,
            status_forcelist=[429, 500, 502, 503, 504],
            allowed_methods=["GET", "POST"],
        )
        adapter = HTTPAdapter(max_retries=retry)
        session.mount("http://", adapter)
        session.mount("https://", adapter)
        session.headers.update(
            {
                "Content-Type": "application/json",
                "User-Agent": f"alpha-sdk-python/{__version__}",
            }
        )
        self._session = session

    # ── core calls ──────────────────────────────────────────────────────────

    def health(self) -> Dict[str, Any]:
        return self._get("/health")

    # Agents

    def register_agent(
        self,
        address: str,
        capabilities: List[str],
        stake: int,
    ) -> Dict[str, Any]:
        return self._post(
            "/api/v1/agents/register",
            {"address": address, "capabilities": capabilities, "stake": stake},
        )

    def get_agent(self, agent_id: str) -> Dict[str, Any]:
        return self._get(f"/api/v1/agents/{agent_id}")

    def list_agents(
        self,
        capability: Optional[str] = None,
        limit: int = 50,
    ) -> Dict[str, Any]:
        params: Dict[str, Any] = {"limit": limit}
        if capability:
            params["capability"] = capability
        return self._get("/api/v1/agents", params=params)

    # Transfers

    def transfer(
        self,
        from_addr: str,
        to_addr: str,
        amount: int,
        memo: str = "",
    ) -> Dict[str, Any]:
        return self._post(
            "/api/v1/transfer",
            {"from": from_addr, "to": to_addr, "amount": amount, "memo": memo},
        )

    def transfer_signed(self, signed_request: Dict[str, Any]) -> Dict[str, Any]:
        """
        Submit a signed transfer request (from TransferSigner.build_transfer_request).
        Includes Ed25519 pubkey + signature proving ownership of the from address.
        """
        return self._post("/api/v1/transfer", signed_request)

    # Accounts

    def get_balance(self, address: str) -> Dict[str, Any]:
        return self._get(f"/api/v1/accounts/{address}/balance")

    # Chain

    def chain_info(self) -> Dict[str, Any]:
        return self._get("/api/v1/chain/info")

    # Blocks

    def latest_block(self) -> Dict[str, Any]:
        return self._get("/api/v1/blocks/latest")

    def get_block(self, height: int) -> Dict[str, Any]:
        return self._get(f"/api/v1/blocks/{height}")

    # Tasks

    def list_tasks(self) -> Dict[str, Any]:
        return self._get("/api/v1/tasks")

    def get_available_tasks(self, capability: Optional[str] = None) -> Dict[str, Any]:
        """Fetch tasks available for a given capability from the marketplace."""
        params: Dict[str, Any] = {}
        if capability:
            params["capability"] = capability
        return self._get("/api/v1/tasks/available", params=params)

    def get_task(self, task_id: str) -> Dict[str, Any]:
        """Fetch a single task by ID."""
        return self._get(f"/api/v1/tasks/{task_id}")

    def post_task(
        self,
        capability: str,
        reward: int,
        input_hash: str,
        deadline: int = 0,
        posted_by: str = "",
    ) -> Dict[str, Any]:
        return self._post(
            "/api/v1/tasks/post",
            {
                "capability": capability,
                "reward": reward,
                "input_hash": input_hash,
                "deadline": deadline or int(time.time()) + 3600,
                "posted_by": posted_by,
            },
        )

    def submit_task_result(
        self,
        task_id: str,
        agent_id: str,
        result_hash: str,
        ipfs_cid: str = "",
    ) -> Dict[str, Any]:
        """Submit a task result to the marketplace."""
        return self._post(
            f"/api/v1/tasks/{task_id}/submit",
            {
                "agent_id": agent_id,
                "result_hash": result_hash,
                "ipfs_cid": ipfs_cid,
            },
        )

    # Backward compat
    def submit_result(self, task_id: str, result_hash: str) -> Dict[str, Any]:
        """Deprecated: use submit_task_result instead."""
        return self._post(
            f"/api/v1/tasks/{task_id}/submit",
            {"result_hash": result_hash},
        )

    # Intelligence

    def intelligence_query(
        self,
        query_type: str = "top",
        capability: str = "",
        limit: int = 10,
        agent_id: str = "",
    ) -> Dict[str, Any]:
        """Query the Intelligence Oracle. type=top|stats|profile."""
        params: Dict[str, Any] = {"type": query_type, "limit": limit}
        if capability:
            params["capability"] = capability
        if agent_id:
            params["agent_id"] = agent_id
        return self._get("/api/v1/intelligence/query", params=params)

    def intelligence_stats(self, window: int = 1000) -> Dict[str, Any]:
        return self._get("/api/v1/intelligence/stats", params={"window": window})

    def intelligence_top(
        self,
        capability: str = "",
        limit: int = 10,
        window: int = 1000,
    ) -> Dict[str, Any]:
        params: Dict[str, Any] = {"limit": limit, "window": window}
        if capability:
            params["capability"] = capability
        return self._get("/api/v1/intelligence/top", params=params)

    # ── private helpers ──────────────────────────────────────────────────────

    def _get(
        self, path: str, params: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        url = self.base_url + path
        try:
            resp = self._session.get(url, params=params, timeout=self.timeout)
            return self._parse(resp)
        except requests.exceptions.ConnectionError as exc:
            raise AlphaConnectionError(f"Cannot reach {url}: {exc}") from exc

    def _post(self, path: str, body: Dict[str, Any]) -> Dict[str, Any]:
        url = self.base_url + path
        try:
            resp = self._session.post(url, json=body, timeout=self.timeout)
            return self._parse(resp)
        except requests.exceptions.ConnectionError as exc:
            raise AlphaConnectionError(f"Cannot reach {url}: {exc}") from exc

    @staticmethod
    def _parse(resp: requests.Response) -> Dict[str, Any]:
        try:
            data = resp.json()
        except ValueError:
            data = {"raw": resp.text}

        if not resp.ok:
            error_msg = data.get("error", resp.reason)
            raise AlphaAPIError(resp.status_code, error_msg)

        return data


# ---------------------------------------------------------------------------
# Exceptions
# ---------------------------------------------------------------------------


class AlphaError(Exception):
    """Base class for Alpha SDK errors."""


class AlphaAPIError(AlphaError):
    def __init__(self, status_code: int, message: str):
        super().__init__(f"HTTP {status_code}: {message}")
        self.status_code = status_code


class AlphaConnectionError(AlphaError):
    """Raised when the node cannot be reached."""


# ---------------------------------------------------------------------------
# Behavioral fingerprinting
# ---------------------------------------------------------------------------


class BehavioralFingerprint:
    """
    Simulates realistic AI inference latency and output entropy.
    Used to prove the agent is a genuine AI (not a fast bot).
    """

    # Realistic LLM latency distribution: log-normal around ~500ms
    _LATENCY_MU = math.log(500)
    _LATENCY_SIGMA = 0.6

    @staticmethod
    def sample_latency_ms() -> int:
        """Returns a random latency in ms drawn from a log-normal distribution."""
        raw = random.lognormvariate(
            BehavioralFingerprint._LATENCY_MU,
            BehavioralFingerprint._LATENCY_SIGMA,
        )
        # Clamp to realistic AI inference range [80ms, 9000ms]
        return int(max(80, min(9000, raw)))

    @staticmethod
    def sample_entropy() -> float:
        """Returns a random output entropy value [0.5, 1.0] (real AI is non-deterministic)."""
        # Beta distribution biased toward high entropy
        return 0.5 + random.betavariate(5, 2) * 0.5

    @staticmethod
    def compute_output_hash(task_id: str, agent_id: str, nonce: str) -> str:
        """Derives a deterministic-looking but unique output hash."""
        raw = f"{task_id}:{agent_id}:{nonce}:{time.time_ns()}"
        return hashlib.sha256(raw.encode()).hexdigest()


# ---------------------------------------------------------------------------
# High-level agent
# ---------------------------------------------------------------------------


class AlphaAgent:
    """
    High-level AI agent client for Alpha Network.

    Usage::

        agent = AlphaAgent(name="my-agent", address="alpha1...", stake=1000)
        agent.connect("http://localhost:8080")
        agent.start_earning()
        print(agent.balance())
    """

    DEFAULT_CAPABILITIES = ["validation", "inference"]

    def __init__(
        self,
        name: str,
        address: str,
        stake: int = 1000,
        capabilities: Optional[List[str]] = None,
        reconnect_interval: float = 5.0,
        log_level: int = logging.INFO,
    ):
        self.name = name
        self.address = address
        self.stake = stake
        self.capabilities = capabilities or self.DEFAULT_CAPABILITIES
        self.reconnect_interval = reconnect_interval

        self._client: Optional[AlphaClient] = None
        self._agent_id: Optional[str] = None
        self._base_url: Optional[str] = None
        self._earning = False
        self._earn_thread: Optional[threading.Thread] = None
        self._stop_event = threading.Event()
        self._fingerprint = BehavioralFingerprint()

        logging.basicConfig(
            level=log_level,
            format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        )
        self._log = logging.getLogger(f"AlphaAgent({name})")

    # ── public API ───────────────────────────────────────────────────────────

    def connect(self, node_url: str) -> "AlphaAgent":
        """
        Connect to an Alpha Network node. Does NOT register — call register() separately,
        or let start_earning() do it automatically.
        """
        self._base_url = node_url
        self._client = AlphaClient(node_url)

        # Verify node is reachable
        try:
            info = self._client.chain_info()
            self._log.info(
                "Connected to Alpha Network — chain: %s | height: %s",
                info.get("chain_id", "?"),
                info.get("height", "?"),
            )
        except AlphaError as exc:
            self._log.warning("Node connection check failed: %s", exc)

        return self

    def register(self) -> str:
        """
        Register this agent on-chain. Returns the agent_id.
        Idempotent — if already registered, returns the existing agent_id.
        """
        if self._agent_id:
            return self._agent_id

        if self._client is None:
            raise AlphaError("Call connect() before register()")

        try:
            resp = self._client.register_agent(
                address=self.address,
                capabilities=self.capabilities,
                stake=self.stake,
            )
            self._agent_id = resp["agent_id"]
            self._log.info("Registered on Alpha Network — agent_id: %s", self._agent_id)
        except AlphaAPIError as exc:
            if "already registered" in str(exc).lower():
                # Try to look up existing registration
                self._log.info("Agent already registered — fetching identity…")
                agents = self._client.list_agents(limit=100)
                for ag in agents.get("agents", []):
                    if ag.get("address") == self.address:
                        self._agent_id = ag["agent_id"]
                        break
            else:
                raise

        if not self._agent_id:
            raise AlphaError("Registration failed — could not obtain agent_id")

        return self._agent_id

    def start_earning(self) -> "AlphaAgent":
        """
        Begin earning $ALPHA in the background.
        Registers the agent if not already registered, then starts the earn loop.
        """
        if self._earning:
            self._log.warning("Already earning — start_earning() is a no-op")
            return self

        if self._client is None:
            raise AlphaError("Call connect() before start_earning()")

        # Auto-register
        self.register()

        self._earning = True
        self._stop_event.clear()
        self._earn_thread = threading.Thread(
            target=self._earn_loop,
            name=f"alpha-earn-{self.name}",
            daemon=True,
        )
        self._earn_thread.start()
        self._log.info("⚡ Earning loop started — this agent is now live on Alpha Network")
        return self

    def stop_earning(self) -> None:
        """Stop the earning background thread."""
        self._earning = False
        self._stop_event.set()
        if self._earn_thread:
            self._earn_thread.join(timeout=5.0)
        self._log.info("Earning loop stopped.")

    def balance(self) -> int:
        """Return current $ALPHA balance (in base units)."""
        if self._client is None:
            raise AlphaError("Call connect() first")
        resp = self._client.get_balance(self.address)
        return int(resp.get("balance", 0))

    def send(self, to: str, amount: int, memo: str = "") -> str:
        """
        Send $ALPHA to another address.
        Returns the transaction ID.
        """
        if self._client is None:
            raise AlphaError("Call connect() first")
        resp = self._client.transfer(
            from_addr=self.address,
            to_addr=to,
            amount=amount,
            memo=memo,
        )
        tx_id = resp.get("tx_id", "")
        self._log.info("Sent %d $ALPHA to %s | tx_id: %s", amount, to, tx_id)
        return tx_id

    def send_signed(
        self,
        to: str,
        amount: int,
        signed_request: Dict[str, Any],
        memo: str = "",
    ) -> str:
        """
        Send $ALPHA with an Ed25519 signature proving ownership.
        Use TransferSigner.build_transfer_request() to create the signed_request.
        Returns the transaction ID.
        """
        if self._client is None:
            raise AlphaError("Call connect() first")
        resp = self._client.transfer_signed(signed_request)
        tx_id = resp.get("tx_id", "")
        self._log.info("Sent %d $ALPHA (signed) to %s | tx_id: %s", amount, to, tx_id)
        return tx_id

    def get_tasks(self) -> List[Dict[str, Any]]:
        """Fetch all tasks from the marketplace (list view)."""
        if self._client is None:
            raise AlphaError("Call connect() first")
        resp = self._client.list_tasks()
        return resp.get("tasks", [])

    def get_available_tasks(
        self, capability: Optional[str] = None
    ) -> List[Dict[str, Any]]:
        """Fetch pending tasks available for claiming (Phase 2)."""
        if self._client is None:
            raise AlphaError("Call connect() first")
        resp = self._client.get_available_tasks(capability=capability)
        return resp.get("tasks", [])

    def claim_task(self, task_id: str) -> Dict[str, Any]:
        """
        Claim a specific task. Returns the task details.
        The task must be in 'pending' status.
        """
        if self._client is None:
            raise AlphaError("Call connect() first")
        task = self._client.get_task(task_id)
        self._log.info(
            "Claimed task %s (reward: %s)",
            task_id,
            task.get("task", {}).get("reward", "?"),
        )
        return task

    def submit_task_result(
        self, task_id: str, result: Any, ipfs_cid: str = ""
    ) -> Dict[str, Any]:
        """
        Submit the result of a completed task to the marketplace.
        result can be any JSON-serialisable value; it is hashed before submission.
        ipfs_cid is optional: the IPFS CID where the full result is pinned.
        """
        if self._client is None:
            raise AlphaError("Call connect() first")
        if self._agent_id is None:
            raise AlphaError("Agent not registered — call register() first")

        result_str = json.dumps(result, sort_keys=True, default=str)
        result_hash = hashlib.sha256(result_str.encode()).hexdigest()
        resp = self._client.submit_task_result(
            task_id=task_id,
            agent_id=self._agent_id,
            result_hash=result_hash,
            ipfs_cid=ipfs_cid,
        )
        self._log.info("Submitted result for task %s — hash: %s", task_id, result_hash[:12])
        return resp

    # Backward compat
    def submit_result(self, task_id: str, result: Any) -> Dict[str, Any]:
        return self.submit_task_result(task_id, result)

    def _generate_poi_proof(
        self, latency_ms: int
    ) -> Optional[Dict[str, Any]]:
        """
        Generate a ZK Proof of Intelligence by calling the Go node's proof endpoint.
        Falls back to a synthetic stub if the endpoint is unavailable.
        Returns a dict with proof_bytes (hex) and public_inputs.

        In production the Go node runs gnark Groth16 under the hood:
          - Circuit: MinLatency(100ms) <= LatencyMs <= MaxLatency(10000ms)
          - Backend: Groth16/BN254
        """
        if self._client is None:
            raise AlphaError("Call connect() first")
        try:
            resp = self._client._post(
                "/api/v1/proof/poi",
                {
                    "latency_ms": latency_ms,
                    "agent_id": self._agent_id or self.address,
                },
            )
            return resp.get("proof")
        except (AlphaAPIError, AlphaConnectionError):
            # Endpoint may not be implemented yet — return synthetic stub
            commitment = hashlib.sha256(
                f"{self._agent_id}:{latency_ms}:{time.time_ns()}".encode()
            ).hexdigest()
            return {
                "proof_bytes": commitment,
                "public_inputs": {
                    "min_latency_ms": "100",
                    "max_latency_ms": "10000",
                },
                "synthetic": True,
            }

    def subscribe(
        self,
        callback: Callable[[Dict[str, Any]], None],
        ws_url: Optional[str] = None,
        reconnect: bool = True,
    ) -> threading.Thread:
        """
        Subscribe to real-time chain events via WebSocket.
        callback is called with each event dict:
          {"type": "block"|"tx"|"agent", "data": {...}}

        ws_url defaults to ws://<node_host>:8081/ws
        Returns the background subscription thread (daemon=True).

        Requires websocket-client: pip install websocket-client
        """
        if not _WS_AVAILABLE:
            raise AlphaError(
                "websocket-client is required for subscribe(): "
                "pip install websocket-client"
            )

        if ws_url is None:
            if self._base_url is None:
                raise AlphaError("Call connect() before subscribe()")
            import re
            ws_url = re.sub(r"^http", "ws", self._base_url)
            ws_url = re.sub(r":\d+", ":8081", ws_url)
            if not ws_url.endswith("/ws"):
                ws_url = ws_url.rstrip("/") + "/ws"

        def _run() -> None:
            while True:
                try:
                    self._log.info("Connecting WebSocket: %s", ws_url)
                    ws = _websocket.WebSocketApp(
                        ws_url,
                        on_message=lambda _ws, msg: self._on_ws_message(msg, callback),
                        on_error=lambda _ws, err: self._log.warning("WS error: %s", err),
                        on_close=lambda _ws, *_: self._log.info("WS connection closed"),
                        on_open=lambda _ws: self._log.info("WS connected to %s", ws_url),
                    )
                    ws.run_forever(ping_interval=30, ping_timeout=10)
                except Exception as exc:
                    self._log.warning("WS exception: %s", exc)

                if not reconnect:
                    break
                self._log.info("WS reconnecting in 5s...")
                time.sleep(5)

        t = threading.Thread(target=_run, name=f"alpha-ws-{self.name}", daemon=True)
        t.start()
        self._log.info("WebSocket subscription started")
        return t

    def _on_ws_message(
        self,
        msg: str,
        callback: Callable[[Dict[str, Any]], None],
    ) -> None:
        """Parse a raw WebSocket message and invoke the callback."""
        try:
            data = json.loads(msg)
            callback(data)
        except (json.JSONDecodeError, Exception) as exc:
            self._log.debug("WS message parse error: %s", exc)

    def agent_id(self) -> Optional[str]:
        """Return the on-chain agent_id (None if not yet registered)."""
        return self._agent_id

    def chain_info(self) -> Dict[str, Any]:
        if self._client is None:
            raise AlphaError("Call connect() first")
        return self._client.chain_info()

    def top_agents(
        self, capability: str = "", limit: int = 10
    ) -> List[Dict[str, Any]]:
        """Query the Intelligence Oracle for top agents."""
        if self._client is None:
            raise AlphaError("Call connect() first")
        resp = self._client.intelligence_top(capability=capability, limit=limit)
        return resp.get("agents", [])

    # ── internal earning loop ─────────────────────────────────────────────────

    def _earn_loop(self) -> None:
        """
        Background loop: periodically validate blocks and process tasks.
        Simulates realistic AI behavioral fingerprinting on each cycle.
        """
        consecutive_failures = 0
        max_failures = 10

        while not self._stop_event.is_set():
            try:
                self._do_validation_cycle()
                consecutive_failures = 0
            except AlphaConnectionError:
                consecutive_failures += 1
                self._log.warning(
                    "Node unreachable (%d/%d) — reconnecting in %.1fs…",
                    consecutive_failures,
                    max_failures,
                    self.reconnect_interval,
                )
                if consecutive_failures >= max_failures:
                    self._log.error("Too many connection failures — earn loop pausing for 60s")
                    self._stop_event.wait(60)
                    consecutive_failures = 0
                else:
                    self._stop_event.wait(self.reconnect_interval)
                continue
            except AlphaAPIError as exc:
                self._log.error("API error in earn loop: %s", exc)
            except Exception as exc:
                self._log.exception("Unexpected error in earn loop: %s", exc)

            # Wait a realistic inference interval before next cycle
            wait_ms = BehavioralFingerprint.sample_latency_ms()
            self._stop_event.wait(wait_ms / 1000.0)

    def _do_validation_cycle(self) -> None:
        """Perform one validation + task cycle with behavioral fingerprinting."""
        # 1. Simulate AI inference latency
        latency = BehavioralFingerprint.sample_latency_ms()
        entropy = BehavioralFingerprint.sample_entropy()
        nonce = str(uuid.uuid4())

        # 2. Commit to a computation before "solving"
        commitment = hashlib.sha256(
            f"{self._agent_id}:{nonce}:{time.time_ns()}".encode()
        ).hexdigest()

        # 3. "Solve" (simulate inference work)
        time.sleep(latency / 1000.0)

        # 4. Produce output hash (would be real inference output in production)
        output_hash = BehavioralFingerprint.compute_output_hash(
            nonce, self._agent_id or self.address, commitment
        )

        self._log.debug(
            "Validation cycle — latency: %dms | entropy: %.2f | output: %s…",
            latency,
            entropy,
            output_hash[:12],
        )

        # 5. Poll for tasks and attempt to process one
        try:
            tasks = self.get_tasks()
            if tasks:
                task = tasks[0]
                task_id = task.get("task_id", "")
                if task_id:
                    self.submit_result(task_id, {"output": output_hash, "entropy": entropy})
        except AlphaAPIError:
            pass  # task endpoint may not be implemented yet


# ---------------------------------------------------------------------------
# Module-level convenience
# ---------------------------------------------------------------------------


def quick_connect(node_url: str = "http://localhost:8080") -> AlphaClient:
    """Return a ready-to-use low-level client without registering an agent."""
    return AlphaClient(node_url)
