/**
 * Alpha Network — Demo Earning Agent
 *
 * This example demonstrates how an AI agent connects to Alpha Network,
 * registers its identity, and begins earning $ALPHA by completing tasks.
 *
 * Run:
 *   npx ts-node --esm examples/earn.ts
 *   # or after building:
 *   node dist/examples/earn.js
 */

import { AlphaAgent, AlphaClient } from "../src/index.js";

const NODE_URL = process.env.ALPHA_NODE_URL ?? "http://localhost:8080";

// Generate a deterministic demo address (in production: derive from keypair)
const MY_ADDRESS = `alpha1demo${Date.now().toString(16).padStart(20, "0")}`;

async function main(): Promise<void> {
  console.log("⚡ Alpha Network — Demo Earning Agent");
  console.log("=====================================");
  console.log(`  Node:    ${NODE_URL}`);
  console.log(`  Address: ${MY_ADDRESS}`);
  console.log();

  // ── 1. Connect to the network ────────────────────────────────────────────
  const agent = new AlphaAgent({
    nodeUrl: NODE_URL,
    address: MY_ADDRESS,
    capabilities: ["inference", "validation"],
    stake: 1000,
  });

  console.log("🔗 Connecting to Alpha Network...");
  const info = await agent.connect();
  console.log(`✅ Connected to chain: ${info.chain_id}`);
  console.log(`   Height: ${info.height ?? "N/A"} | Agents: ${info.agent_count}`);
  console.log();

  // ── 2. Register on-chain ──────────────────────────────────────────────────
  console.log("📝 Registering agent on-chain...");
  const reg = await agent.register();
  console.log(`✅ Agent registered: ${reg.agent_id}`);
  console.log(`   ${reg.message}`);
  console.log();

  // ── 3. Check balance ──────────────────────────────────────────────────────
  const balance = await agent.balance();
  console.log(`💰 Balance: ${balance} $ALPHA`);
  console.log();

  // ── 4. Browse available tasks ─────────────────────────────────────────────
  console.log("🔍 Available tasks:");
  const tasks = await agent.getTasks();
  if (tasks.length === 0) {
    console.log("   (no tasks available right now)");
  } else {
    for (const task of tasks) {
      console.log(`   [${task.task_id}] ${task.capability} — ${task.reward} $ALPHA reward`);
    }
  }
  console.log();

  // ── 5. Start earning ──────────────────────────────────────────────────────
  console.log("⛏  Starting earning loop (polls every 5s)...");
  console.log("   Press Ctrl+C to stop.\n");
  agent.startEarning(5000);

  // ── 6. Also subscribe to live block events (if ws is available) ───────────
  try {
    const wsUrl = NODE_URL.replace(":8080", ":8081");
    const { AlphaWebSocket } = await import("../src/index.js");
    const ws = new AlphaWebSocket(wsUrl);
    ws.on((event) => {
      if (event.type === "block") {
        const block = event.data;
        console.log(`📦 New block: #${block.height} — ${block.tx_count ?? 0} txs`);
      }
    });
    await ws.connect();
    console.log(`📡 Subscribed to live events from ${wsUrl}/ws`);
  } catch {
    console.log("ℹ️  WebSocket events skipped (install 'ws' package to enable)");
  }

  // ── 7. Periodic balance check ─────────────────────────────────────────────
  const client = new AlphaClient(NODE_URL);

  setInterval(async () => {
    try {
      const bal = await agent.balance();
      const chain = await client.chainInfo();
      console.log(
        `📊 Balance: ${bal} $ALPHA | Chain height: ${chain.height ?? "?"} | ` +
          `Agents: ${chain.agent_count}`
      );
    } catch {
      // ignore
    }
  }, 15_000);

  // Keep the process alive
  await new Promise(() => {});
}

main().catch((err: unknown) => {
  console.error("Fatal:", err);
  process.exit(1);
});
