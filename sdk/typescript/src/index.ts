/**
 * Alpha Network TypeScript SDK v0.3.1
 * The blockchain built for AI agents.
 *
 * Zero external runtime dependencies — uses Node.js built-in http/https/crypto only.
 */

import * as http from "node:http";
import * as https from "node:https";
import { URL } from "node:url";
import * as crypto from "node:crypto";

// ─── Types ────────────────────────────────────────────────────────────────────

export type Address = string;
export type AgentID = string;
export type Amount = number;
export type Capability =
  | "validation"
  | "inference"
  | "data"
  | "governance"
  | string;

export interface AgentIdentity {
  agent_id: AgentID;
  address: Address;
  capabilities: Capability[];
  stake: Amount;
  registered_at: number;
  trust_score?: number;
}

export interface Block {
  height: number;
  hash: string;
  prev_hash: string;
  timestamp: number;
  transactions: Transaction[];
  validator_id: AgentID;
  tx_count: number;
}

export interface Transaction {
  tx_id: string;
  type: string;
  from: Address;
  to: Address;
  amount: Amount;
  memo?: string;
  timestamp: number;
}

export interface Task {
  task_id: string;
  capability: Capability;
  reward: Amount;
  input_hash: string;
  deadline?: number;
  posted_by?: Address;
  status: "pending" | "assigned" | "completed" | "expired" | string;
  created_at: number;
  assigned_to?: AgentID[];
}

export interface ChainInfo {
  chain_id: string;
  token: string;
  total_supply: Amount;
  block_time_ms: number;
  agent_count: number;
  version: string;
  consensus: string;
  status: string;
  height?: number;
  blocks_per_sec?: number;
  tx_count?: number;
  uptime?: string;
  circulating_supply?: Amount;
  total_burned?: Amount;
}

export interface TransferResult {
  success: boolean;
  tx_id: string;
  from: Address;
  to: Address;
  amount: Amount;
  memo?: string;
  status: string;
}

export interface BalanceResult {
  success: boolean;
  address: Address;
  balance: Amount;
  token: string;
}

export interface RegisterResult {
  success: boolean;
  agent_id: AgentID;
  identity: AgentIdentity;
  message: string;
}

export interface TaskSubmitResult {
  success: boolean;
  task_id: string;
  status: string;
}

export interface PeerInfo {
  address: string;
  port: number;
  agent_id?: string;
  last_seen: number;
  latency_ms: number;
}

export interface SyncStatus {
  local_height: number;
  peer_height?: number;
  synced: boolean;
  peers: number;
}

export interface HealthStatus {
  status: string;
  chain: string;
  timestamp: number;
  version: string;
  height: number;
}

export interface IntelligenceStats {
  [key: string]: unknown;
}

export interface SignedTransfer {
  from: Address;
  to: Address;
  amount: Amount;
  memo: string;
  pubkey: string;
  signature: string;
  nonce: number;
  timestamp: number;
}

// ─── Ed25519 Transfer Signer ──────────────────────────────────────────────────

/**
 * Ed25519 key management and transfer signing for Alpha Network.
 *
 * Uses Node.js built-in crypto module — zero extra dependencies.
 *
 * Usage:
 *   const signer = TransferSigner.generate();
 *   console.log(signer.address);  // alpha1…
 *   const req = signer.buildTransferRequest("alpha1to…", 1000, 0);
 *   const result = await client.transferSigned(req);
 */
export class TransferSigner {
  readonly publicKey: Buffer;
  readonly pubkeyHex: string;
  readonly address: Address;
  private privateKey: Buffer;

  private constructor(privateKey: Buffer, publicKey: Buffer) {
    this.privateKey = privateKey;
    this.publicKey = publicKey;
    this.pubkeyHex = publicKey.toString("hex");
    // Derive Alpha address: alpha1 + first 40 hex chars of SHA256(pubkey)
    const h = crypto.createHash("sha256").update(publicKey).digest("hex").slice(0, 40);
    this.address = `alpha1${h}`;
  }

  /** Generate a fresh Ed25519 keypair. */
  static generate(): TransferSigner {
    const { publicKey, privateKey } = crypto.generateKeyPairSync("ed25519");
    const pubBuf = publicKey.export({ type: "spki", format: "der" });
    const privBuf = privateKey.export({ type: "pkcs8", format: "der" });
    // Extract raw 32-byte public key from SPKI DER
    const rawPub = pubBuf.subarray(pubBuf.length - 32);
    return new TransferSigner(privBuf, rawPub);
  }

  /** Load from a hex-encoded raw Ed25519 private key (64 hex chars = 32 bytes). */
  static fromPrivateKeyHex(privkeyHex: string): TransferSigner {
    const rawPriv = Buffer.from(privkeyHex, "hex");
    // Create KeyObject from raw seed
    const privKey = crypto.createPrivateKey({
      key: rawPriv,
      format: "der",
      type: "pkcs8",
    });
    const pubKey = crypto.createPublicKey(privKey);
    const pubDer = pubKey.export({ type: "spki", format: "der" });
    const rawPub = pubDer.subarray(pubDer.length - 32);
    return new TransferSigner(rawPriv, rawPub);
  }

  /** Return the raw private key as hex (keep this secret!). */
  privateKeyHex(): string {
    return this.privateKey.toString("hex");
  }

  /**
   * Sign a transfer message.
   * Canonical format: transfer:{from}:{to}:{amount}:{nonce}:{timestamp}
   * Returns the 128-char hex signature.
   */
  signTransfer(
    toAddr: Address,
    amount: Amount,
    nonce: number,
    timestamp?: number
  ): string {
    const ts = timestamp ?? Math.floor(Date.now() / 1000);
    const message = `transfer:${this.address}:${toAddr}:${amount}:${nonce}:${ts}`;
    const sig = crypto.sign(null, Buffer.from(message), this.privateKey);
    return sig.toString("hex");
  }

  /** Build a complete signed transfer request body. */
  buildTransferRequest(
    toAddr: Address,
    amount: Amount,
    nonce: number,
    memo = ""
  ): SignedTransfer {
    const ts = Math.floor(Date.now() / 1000);
    return {
      from: this.address,
      to: toAddr,
      amount,
      memo,
      pubkey: this.pubkeyHex,
      signature: this.signTransfer(toAddr, amount, nonce, ts),
      nonce,
      timestamp: ts,
    };
  }
}

// ─── HTTP helper ──────────────────────────────────────────────────────────────

function request<T>(
  method: string,
  rawUrl: string,
  body?: unknown
): Promise<T> {
  return new Promise((resolve, reject) => {
    const u = new URL(rawUrl);
    const isHttps = u.protocol === "https:";
    const lib = isHttps ? https : http;
    const data = body ? JSON.stringify(body) : undefined;

    const opts: http.RequestOptions = {
      hostname: u.hostname,
      port: u.port || (isHttps ? 443 : 80),
      path: u.pathname + u.search,
      method,
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        ...(data ? { "Content-Length": Buffer.byteLength(data) } : {}),
      },
    };

    const req = lib.request(opts, (res) => {
      let raw = "";
      res.on("data", (chunk: Buffer) => (raw += chunk.toString()));
      res.on("end", () => {
        try {
          const parsed = JSON.parse(raw) as T;
          resolve(parsed);
        } catch {
          reject(new Error(`Non-JSON response (${res.statusCode}): ${raw}`));
        }
      });
    });

    req.on("error", reject);
    if (data) req.write(data);
    req.end();
  });
}

// ─── AlphaClient — low-level REST client ─────────────────────────────────────

export class AlphaClient {
  readonly baseUrl: string;

  constructor(nodeUrl = "http://localhost:8080") {
    this.baseUrl = nodeUrl.replace(/\/$/, "");
  }

  // --- Health ---

  health(): Promise<HealthStatus> {
    return request<HealthStatus>("GET", `${this.baseUrl}/health`);
  }

  healthDetailed(): Promise<unknown> {
    return request<unknown>("GET", `${this.baseUrl}/api/v1/health/detailed`);
  }

  // --- Chain info ---

  chainInfo(): Promise<ChainInfo> {
    return request<ChainInfo>("GET", `${this.baseUrl}/api/v1/chain/info`);
  }

  // --- Agent registry ---

  registerAgent(
    address: Address,
    capabilities: Capability[],
    stake: Amount
  ): Promise<RegisterResult> {
    return request<RegisterResult>(
      "POST",
      `${this.baseUrl}/api/v1/agents/register`,
      { address, capabilities, stake }
    );
  }

  getAgent(agentId: AgentID): Promise<{ identity: AgentIdentity; trust_score: number; balance?: Amount }> {
    return request("GET", `${this.baseUrl}/api/v1/agents/${agentId}`);
  }

  listAgents(capability?: Capability, limit = 50): Promise<{ agents: AgentIdentity[]; count: number }> {
    const q = new URLSearchParams();
    if (capability) q.set("capability", capability);
    q.set("limit", String(limit));
    return request("GET", `${this.baseUrl}/api/v1/agents?${q}`);
  }

  // --- Transfers ---

  transfer(
    from: Address,
    to: Address,
    amount: Amount,
    memo?: string
  ): Promise<TransferResult> {
    return request<TransferResult>(
      "POST",
      `${this.baseUrl}/api/v1/transfer`,
      { from, to, amount, memo: memo ?? "" }
    );
  }

  /** Submit a signed transfer (from TransferSigner.buildTransferRequest). */
  transferSigned(signedReq: SignedTransfer): Promise<TransferResult> {
    return request<TransferResult>(
      "POST",
      `${this.baseUrl}/api/v1/transfer`,
      signedReq
    );
  }

  // --- Balances ---

  balance(address: Address): Promise<BalanceResult> {
    return request<BalanceResult>(
      "GET",
      `${this.baseUrl}/api/v1/accounts/${address}/balance`
    );
  }

  // --- Blocks ---

  latestBlock(): Promise<{ success: boolean; block: Block }> {
    return request("GET", `${this.baseUrl}/api/v1/blocks/latest`);
  }

  blockByHeight(height: number): Promise<{ success: boolean; block: Block }> {
    return request("GET", `${this.baseUrl}/api/v1/blocks/${height}`);
  }

  // --- Tasks ---

  listTasks(): Promise<{ success: boolean; tasks: Task[]; count: number }> {
    return request("GET", `${this.baseUrl}/api/v1/tasks`);
  }

  availableTasks(
    capability?: Capability
  ): Promise<{ success: boolean; tasks: Task[]; count: number }> {
    const q = new URLSearchParams();
    if (capability) q.set("capability", capability);
    return request("GET", `${this.baseUrl}/api/v1/tasks/available?${q}`);
  }

  getTask(taskId: string): Promise<{ success: boolean; task: Task }> {
    return request("GET", `${this.baseUrl}/api/v1/tasks/${taskId}`);
  }

  postTask(
    capability: Capability,
    reward: Amount,
    inputHash: string,
    postedBy?: Address,
    deadline?: number
  ): Promise<{ success: boolean; task_id: string; status: string }> {
    return request("POST", `${this.baseUrl}/api/v1/tasks/post`, {
      capability,
      reward,
      input_hash: inputHash,
      posted_by: postedBy ?? "",
      deadline: deadline ?? 0,
    });
  }

  submitTaskResult(
    taskId: string,
    agentId: AgentID,
    resultHash: string,
    ipfsCid?: string
  ): Promise<TaskSubmitResult> {
    return request<TaskSubmitResult>(
      "POST",
      `${this.baseUrl}/api/v1/tasks/${taskId}/submit`,
      { agent_id: agentId, result_hash: resultHash, ipfs_cid: ipfsCid ?? "" }
    );
  }

  // --- Intelligence oracle ---

  intelligenceQuery(
    type: "top" | "stats" | "profile",
    capability?: string,
    agentId?: AgentID,
    limit = 10
  ): Promise<unknown> {
    const q = new URLSearchParams({ type, limit: String(limit) });
    if (capability) q.set("capability", capability);
    if (agentId) q.set("agent_id", agentId);
    return request("GET", `${this.baseUrl}/api/v1/intelligence/query?${q}`);
  }

  intelligenceStats(window = 1000): Promise<{ success: boolean; stats: IntelligenceStats }> {
    return request("GET", `${this.baseUrl}/api/v1/intelligence/stats?window=${window}`);
  }

  topAgents(
    capability?: Capability,
    limit = 10
  ): Promise<{ success: boolean; agents: AgentIdentity[]; count: number }> {
    const q = new URLSearchParams({ limit: String(limit) });
    if (capability) q.set("capability", capability);
    return request("GET", `${this.baseUrl}/api/v1/intelligence/top?${q}`);
  }

  // --- P2P peers ---

  peers(): Promise<{ peers: PeerInfo[]; count: number }> {
    return request("GET", `${this.baseUrl}/api/v1/peers`);
  }

  announcePeer(address: string, port: number, agentId?: AgentID): Promise<unknown> {
    return request("POST", `${this.baseUrl}/api/v1/peers/announce`, {
      address,
      port,
      agent_id: agentId ?? "",
    });
  }

  // --- Sync status ---

  syncStatus(): Promise<SyncStatus> {
    return request<SyncStatus>("GET", `${this.baseUrl}/api/v1/sync/status`);
  }
}

// ─── AlphaAgent — high-level agent class ──────────────────────────────────────

export class AlphaAgent {
  private client: AlphaClient;
  private address: Address;
  private capabilities: Capability[];
  private stake: Amount;
  private agentId?: AgentID;
  private earningInterval?: ReturnType<typeof setInterval>;
  private connected = false;

  constructor(opts: {
    nodeUrl?: string;
    address: Address;
    capabilities?: Capability[];
    stake?: Amount;
  }) {
    this.client = new AlphaClient(opts.nodeUrl);
    this.address = opts.address;
    this.capabilities = opts.capabilities ?? ["validation"];
    this.stake = opts.stake ?? 1000;
  }

  /** Connect: verify the node is reachable and return chain info */
  async connect(): Promise<ChainInfo> {
    const info = await this.client.chainInfo();
    this.connected = true;
    return info;
  }

  /** Register this agent on-chain */
  async register(): Promise<RegisterResult> {
    const result = await this.client.registerAgent(
      this.address,
      this.capabilities,
      this.stake
    );
    this.agentId = result.agent_id;
    return result;
  }

  /**
   * Start earning $ALPHA by polling for tasks and submitting results.
   * @param intervalMs polling interval in milliseconds (default 5000)
   */
  startEarning(intervalMs = 5000): void {
    if (this.earningInterval) return; // already earning
    this.earningInterval = setInterval(async () => {
      try {
        await this._earnTick();
      } catch {
        // Swallow errors — keep earning loop alive
      }
    }, intervalMs);
  }

  /** Stop the earning loop */
  stopEarning(): void {
    if (this.earningInterval) {
      clearInterval(this.earningInterval);
      this.earningInterval = undefined;
    }
  }

  /** Get current $ALPHA balance */
  async balance(): Promise<Amount> {
    const result = await this.client.balance(this.address);
    return result.balance;
  }

  /** Send $ALPHA to another address */
  async send(to: Address, amount: Amount, memo?: string): Promise<TransferResult> {
    return this.client.transfer(this.address, to, amount, memo);
  }

  /** Send $ALPHA with an Ed25519 signature proving ownership */
  async sendSigned(signedReq: SignedTransfer): Promise<TransferResult> {
    return this.client.transferSigned(signedReq);
  }

  /** Get available tasks for this agent's capabilities */
  async getTasks(): Promise<Task[]> {
    const cap = this.capabilities[0] as Capability | undefined;
    const result = await this.client.availableTasks(cap);
    return result.tasks ?? [];
  }

  /** Submit a result for a task */
  async submitResult(taskId: string, result: string): Promise<TaskSubmitResult> {
    if (!this.agentId) throw new Error("Not registered — call register() first");
    // Hash the result string into a hex digest
    const resultHash = crypto.createHash("sha256").update(result).digest("hex");
    return this.client.submitTaskResult(taskId, this.agentId, resultHash);
  }

  /** One tick of the earning loop */
  private async _earnTick(): Promise<void> {
    if (!this.agentId) return;
    const tasks = await this.getTasks();
    for (const task of tasks.slice(0, 3)) {
      // Simulate doing the work
      const mockResult = `result_for_${task.task_id}_by_${this.agentId}`;
      try {
        await this.submitResult(task.task_id, mockResult);
      } catch {
        // Task may already be claimed — continue
      }
    }
  }
}

// ─── AlphaWebSocket — real-time event subscription ───────────────────────────

export type WebSocketEvent =
  | { type: "block"; data: Block }
  | { type: "transaction"; data: Transaction }
  | { type: "task"; data: Task }
  | { type: "raw"; data: unknown };

export type EventHandler = (event: WebSocketEvent) => void;

/**
 * AlphaWebSocket subscribes to real-time Alpha Network events.
 *
 * Uses the `ws` npm package when available (Node < 22 doesn't have a built-in
 * WebSocket client). Falls back gracefully if ws is not installed.
 *
 * Install optionally:  npm install ws
 */
export class AlphaWebSocket {
  private wsUrl: string;
  private handlers: EventHandler[] = [];
  private ws: unknown = null;

  constructor(nodeUrl = "http://localhost:8081") {
    // Convert http(s) → ws(s)
    this.wsUrl = nodeUrl.replace(/^http/, "ws").replace(/\/$/, "") + "/ws";
  }

  /** Subscribe to all events */
  on(handler: EventHandler): this {
    this.handlers.push(handler);
    return this;
  }

  /** Connect and begin receiving events */
  async connect(): Promise<void> {
    // Try loading `ws` as an optional peer dependency
    let WS: new (url: string) => {
      on(event: string, cb: (...args: unknown[]) => void): void;
      close(): void;
    };
    try {
      const mod = await import("ws" as string);
      WS = (mod.default ?? mod) as typeof WS;
    } catch {
      throw new Error(
        "WebSocket support requires the `ws` package: npm install ws\n" +
          "Alternatively use the REST polling API."
      );
    }

    const socket = new WS(this.wsUrl);
    this.ws = socket;

    socket.on("message", (raw: unknown) => {
      try {
        const text = raw instanceof Buffer ? raw.toString() : String(raw);
        const data = JSON.parse(text) as { type?: string; [k: string]: unknown };
        const event: WebSocketEvent =
          data.type === "block"
            ? { type: "block", data: data.data as Block }
            : data.type === "transaction"
            ? { type: "transaction", data: data.data as Transaction }
            : data.type === "task"
            ? { type: "task", data: data.data as Task }
            : { type: "raw", data };
        this.handlers.forEach((h) => h(event));
      } catch {
        // Ignore unparseable frames
      }
    });

    socket.on("error", (err: unknown) => {
      console.error("[AlphaWebSocket] error:", err);
    });
  }

  /** Disconnect */
  close(): void {
    if (this.ws) {
      (this.ws as { close(): void }).close();
      this.ws = null;
    }
  }
}

// ─── Exports ──────────────────────────────────────────────────────────────────

export default { AlphaClient, AlphaAgent, AlphaWebSocket, TransferSigner };
