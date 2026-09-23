#!/usr/bin/env node
/**
 * Alpha Network — SPL Token Deployment on Solana Devnet
 *
 * Deploys the $ALPHA token using @solana/web3.js and @solana/spl-token.
 * No team/founder/VC allocation. 100% community via protocol.
 *
 * Usage:
 *   node deploy_spl_token.js
 *   node deploy_spl_token.js --network devnet
 */

const {
  Connection, Keypair, PublicKey, clusterApiUrl,
  sendAndConfirmTransaction, Transaction,
  ComputeBudgetProgram,
} = require('@solana/web3.js');

const {
  createMint, getOrCreateAssociatedTokenAccount,
  mintTo, getMint, TOKEN_PROGRAM_ID,
  createSetAuthorityInstruction, AuthorityType,
} = require('@solana/spl-token');

const fs = require('fs');
const path = require('path');

// ─── Configuration ──────────────────────────────────────────────────────────

const TOKEN_CONFIG = {
  name: 'Alpha Network',
  symbol: 'ALPHA',
  decimals: 9,
  totalSupply: 1_000_000_000,  // 1 billion, in token units (not raw)
  description: 'The native economic layer for AI agents',
  website: 'https://alphanetx.xyz',
  image: 'https://alphanetx.xyz/logo.png',
};

// Supply expressed in raw base units (1 ALPHA = 10^9 base units)
const RAW_SUPPLY = BigInt(TOKEN_CONFIG.totalSupply) * BigInt(10 ** TOKEN_CONFIG.decimals);

// Keypair path — generate automatically if missing
const KEYPAIR_PATH = path.join(process.env.HOME || '/root', '.config/solana', 'alpha-deploy.json');

function loadOrCreateKeypair() {
  const dir = path.dirname(KEYPAIR_PATH);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
  if (fs.existsSync(KEYPAIR_PATH)) {
    const raw = JSON.parse(fs.readFileSync(KEYPAIR_PATH, 'utf8'));
    const kp = Keypair.fromSecretKey(Uint8Array.from(raw));
    console.log(`🔑 Loaded existing keypair: ${kp.publicKey.toBase58()}`);
    return kp;
  }
  const kp = Keypair.generate();
  fs.writeFileSync(KEYPAIR_PATH, JSON.stringify(Array.from(kp.secretKey)));
  console.log(`🔑 Generated new keypair: ${kp.publicKey.toBase58()}`);
  console.log(`   Saved to: ${KEYPAIR_PATH}`);
  return kp;
}

async function main() {
  const args = process.argv.slice(2);
  const isMainnet = args.includes('--network') && args.includes('mainnet-beta');

  const network = isMainnet ? 'mainnet-beta' : 'devnet';
  if (isMainnet) {
    console.log('⚠️  WARNING: Deploying to MAINNET. Real SOL will be spent.');
    console.log('   This should only be done after devnet verification.');
    console.log('   Press Ctrl+C within 10 seconds to abort...');
    await new Promise(r => setTimeout(r, 10000));
  }

  const connection = new Connection(clusterApiUrl(network), 'confirmed');
  const payer = loadOrCreateKeypair();

  console.log(`\n📡 Connected to Solana ${network}`);
  console.log(`   RPC: ${connection.rpcEndpoint}`);

  // ── Check balance ──────────────────────────────────────────────────────
  const balance = await connection.getBalance(payer.publicKey);
  console.log(`💰 Balance: ${balance / 1e9} SOL`);

  if (network === 'devnet' && balance < 0.5 * 1e9) {
    console.log('🪂 Requesting devnet airdrop (2 SOL)...');
    const sig = await connection.requestAirdrop(payer.publicKey, 2 * 1e9);
    await connection.confirmTransaction(sig, 'confirmed');
    const newBalance = await connection.getBalance(payer.publicKey);
    console.log(`   New balance: ${newBalance / 1e9} SOL`);
  }

  if (isMainnet && balance < 0.1 * 1e9) {
    console.error('❌ Insufficient SOL for mainnet deployment. Need at least 0.1 SOL.');
    process.exit(1);
  }

  // ── Create token mint ──────────────────────────────────────────────────
  console.log('\n🪙 Creating SPL token mint...');
  const mint = await createMint(
    connection,
    payer,
    payer.publicKey,  // mint authority
    null,             // no freeze authority
    TOKEN_CONFIG.decimals,
    undefined,
    { commitment: 'confirmed' },
    TOKEN_PROGRAM_ID,
  );
  console.log(`✅ Token mint created: ${mint.toBase58()}`);

  // Verify mint
  const mintInfo = await getMint(connection, mint);
  console.log(`   Decimals: ${mintInfo.decimals}`);
  console.log(`   Mint authority: ${mintInfo.mintAuthority?.toBase58() || 'none'}`);
  console.log(`   Supply: ${mintInfo.supply}`);

  // ── Create token account ───────────────────────────────────────────────
  console.log('\n📂 Creating associated token account...');
  const tokenAccount = await getOrCreateAssociatedTokenAccount(
    connection,
    payer,
    mint,
    payer.publicKey,
    false,
    'confirmed',
  );
  console.log(`✅ Token account: ${tokenAccount.address.toBase58()}`);

  // ── Mint full supply ───────────────────────────────────────────────────
  console.log(`\n💰 Minting ${TOKEN_CONFIG.totalSupply.toLocaleString()} ${TOKEN_CONFIG.symbol}...`);
  console.log(`   Raw amount: ${RAW_SUPPLY.toString()} base units`);

  const mintSig = await mintTo(
    connection,
    payer,
    mint,
    tokenAccount.address,
    payer,
    RAW_SUPPLY,
    [],
    { commitment: 'confirmed' },
  );
  console.log(`✅ Mint transaction: ${mintSig}`);

  // Verify supply
  const updatedMint = await getMint(connection, mint);
  console.log(`   Current supply: ${Number(updatedMint.supply) / 10 ** TOKEN_CONFIG.decimals} ${TOKEN_CONFIG.symbol}`);

  // ── Revoke mint authority (enforce fixed supply) ───────────────────────
  console.log('\n🔒 Revoking mint authority (fixed supply enforcement)...');
  const revokeIx = createSetAuthorityInstruction(
    mint,
    payer.publicKey,
    AuthorityType.MintTokens,
    null,  // null = revoke
    [],
    TOKEN_PROGRAM_ID,
  );

  const revokeTx = new Transaction().add(revokeIx);
  const revokeSig = await sendAndConfirmTransaction(
    connection,
    revokeTx,
    [payer],
    { commitment: 'confirmed' },
  );
  console.log(`✅ Mint authority revoked: ${revokeSig}`);

  const finalMint = await getMint(connection, mint);
  console.log(`   Mint authority now: ${finalMint.mintAuthority?.toBase58() || 'NONE — fixed supply enforced'}`);

  // ── Output deployment info ─────────────────────────────────────────────
  const deploymentInfo = {
    network,
    tokenName: TOKEN_CONFIG.name,
    symbol: TOKEN_CONFIG.symbol,
    decimals: TOKEN_CONFIG.decimals,
    totalSupply: TOKEN_CONFIG.totalSupply,
    rawSupply: RAW_SUPPLY.toString(),
    mint: mint.toBase58(),
    tokenAccount: tokenAccount.address.toBase58(),
    mintAuthority: 'REVOKED',
    deployer: payer.publicKey.toBase58(),
    explorer: isMainnet
      ? `https://solscan.io/token/${mint.toBase58()}`
      : `https://solscan.io/token/${mint.toBase58()}?cluster=devnet`,
    deployedAt: new Date().toISOString(),
  };

  const outputFile = path.join(__dirname, `alpha-token-${network}.json`);
  fs.writeFileSync(outputFile, JSON.stringify(deploymentInfo, null, 2));
  console.log(`\n💾 Deployment info saved to: ${outputFile}`);

  // ── Summary ────────────────────────────────────────────────────────────
  console.log('\n' + '═'.repeat(60));
  console.log('🎉 $ALPHA TOKEN DEPLOYED ON SOLANA!');
  console.log('═'.repeat(60));
  console.log(`  Network:       ${network}`);
  console.log(`  Token Symbol:  $${deploymentInfo.symbol}`);
  console.log(`  Decimals:      ${deploymentInfo.decimals}`);
  console.log(`  Total Supply:  ${deploymentInfo.totalSupply.toLocaleString()}`);
  console.log(`  Mint Address:  ${deploymentInfo.mint}`);
  console.log(`  Mint Auth:     REVOKED (fixed supply)`);
  console.log(`  Explorer:      ${deploymentInfo.explorer}`);
  console.log('═'.repeat(60));
  console.log('\nNext steps:');
  console.log('  1. Verify the token on Solscan');
  console.log('  2. Add mint address to the protocol (go.mod / main.go / SDK)');
  console.log('  3. Set up bridging between Alpha L1 and Solana SPL');
  console.log('  4. Create liquidity pools when ready for mainnet');
  console.log('═'.repeat(60));

  return deploymentInfo;
}

main().catch(err => {
  console.error('\n❌ Deployment failed:', err.message || err);
  if (err.logs) console.error('Logs:', err.logs);
  process.exit(1);
});
