// Alpha Network CLI — single-binary developer toolkit
// Install: curl -sSL alphanetx.xyz/install.sh | bash
// Usage:   alpha <command> [flags]
//
// Commands:
//   connect                  Show chain info
//   faucet     --address     Request testnet $ALPHA
//   register   --address --capabilities  Register an AI agent
//   balance    --address     Show $ALPHA balance
//   earn       --address     Start earning loop (PoI)
//   status     --address     Show agent status, tier, reputation
//   transfer   --from --to --amount --key  Send $ALPHA (Ed25519 signed)
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultNode = "https://alphanetx.xyz"

var nodeURL string

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	nodeURL = os.Getenv("ALPHA_NODE")
	if nodeURL == "" {
		nodeURL = defaultNode
	}

	cmd := os.Args[1]
	os.Args = os.Args[1:] // shift so flag packages parse subcommand flags

	switch cmd {
	case "connect":
		cmdConnect()
	case "faucet":
		cmdFaucet()
	case "register":
		cmdRegister()
	case "balance":
		cmdBalance()
	case "earn":
		cmdEarn()
	case "status":
		cmdStatus()
	case "transfer":
		cmdTransfer()
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`
⚡ Alpha Network CLI

Usage:
  alpha connect                      Show chain info
  alpha faucet    --address ADDR     Request testnet $ALPHA
  alpha register  --address ADDR --capabilities LIST  Register an agent
  alpha balance   --address ADDR     Show $ALPHA balance
  alpha earn      --address ADDR     Start earning loop (PoI)
  alpha status    --address ADDR     Show agent status
  alpha transfer  --from ADDR --to ADDR --amount N --key HEX  Send $ALPHA

Environment:
  ALPHA_NODE    API node URL (default: https://alphanetx.xyz)

Install:  curl -sSL https://alphanetx.xyz/install.sh | bash
`)
}

// ─── API Helpers ─────────────────────────────────────────────────────────────

func apiGet(path string) (map[string]interface{}, error) {
	url := strings.TrimRight(nodeURL, "/") + path
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("bad JSON: %s", snippet)
	}
	return data, nil
}

func apiPost(path string, body interface{}) (map[string]interface{}, error) {
	url := strings.TrimRight(nodeURL, "/") + path
	payload, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("bad JSON: %s", snippet)
	}
	return data, nil
}

// ─── Commands ────────────────────────────────────────────────────────────────

func cmdConnect() {
	fmt.Println("⚡ Alpha Network CLI")
	fmt.Println()

	data, err := apiGet("/api/v1/chain/info")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Could not connect to %s: %v\n", nodeURL, err)
		os.Exit(1)
	}

	fmt.Printf("  Node:       %s\n", nodeURL)
	fmt.Printf("  Chain ID:   %v\n", data["chain_id"])
	fmt.Printf("  Height:     %v\n", data["height"])
	fmt.Printf("  Consensus:  %v\n", data["consensus"])
	fmt.Printf("  Version:    %v\n", data["version"])
	fmt.Printf("  Token:      %v\n", data["token"])
	fmt.Printf("  Supply:     %v\n", data["total_supply"])
	fmt.Printf("  Agents:     %v\n", data["agent_count"])
	fmt.Printf("  Status:     %v\n", data["status"])
	fmt.Println()
	fmt.Println("✅ Connected to Alpha Network")
}

func cmdFaucet() {
	fs := flag.NewFlagSet("faucet", flag.ExitOnError)
	address := fs.String("address", "", "Alpha bech32 address (alpha1...)")
	fs.Parse(os.Args[1:])

	if *address == "" {
		fmt.Fprintln(os.Stderr, "❌ --address required (alpha1...)")
		os.Exit(1)
	}

	payload := map[string]string{"address": *address}
	data, err := apiPost("/api/v1/faucet/send", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Faucet request failed: %v\n", err)
		os.Exit(1)
	}

	if data["success"] == true {
		fmt.Printf("✅ %v\n", data["message"])
		if tx, ok := data["tx_id"]; ok {
			fmt.Printf("   Tx: %v\n", tx)
		}
	} else {
		fmt.Fprintf(os.Stderr, "❌ %v\n", data["error"])
		os.Exit(1)
	}
}

func cmdRegister() {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	address := fs.String("address", "", "Alpha bech32 address (alpha1...)")
	caps := fs.String("capabilities", "validation", "Comma-separated: inference,validation,data,governance")
	fs.Parse(os.Args[1:])

	if *address == "" {
		fmt.Fprintln(os.Stderr, "❌ --address required (alpha1...)")
		os.Exit(1)
	}

	capList := strings.Split(*caps, ",")
	for i := range capList {
		capList[i] = strings.TrimSpace(capList[i])
	}

	payload := map[string]interface{}{
		"address":      *address,
		"capabilities": capList,
		"stake":        1000,
	}
	data, err := apiPost("/api/v1/agents/register", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Registration failed: %v\n", err)
		os.Exit(1)
	}

	if data["success"] == true {
		fmt.Printf("✅ Agent registered!\n")
		fmt.Printf("   Agent ID:    %v\n", data["agent_id"])
		fmt.Printf("   Agent #:     %v\n", data["agent_number"])
		fmt.Printf("   Stake req:   %v $ALPHA\n", data["required_stake"])
		fmt.Printf("   Stake locked:%v $ALPHA\n", data["stake_locked"])
	} else {
		fmt.Fprintf(os.Stderr, "❌ %v\n", data["error"])
		os.Exit(1)
	}
}

func cmdBalance() {
	fs := flag.NewFlagSet("balance", flag.ExitOnError)
	address := fs.String("address", "", "Alpha bech32 address (alpha1...)")
	fs.Parse(os.Args[1:])

	if *address == "" {
		fmt.Fprintln(os.Stderr, "❌ --address required (alpha1...)")
		os.Exit(1)
	}

	data, err := apiGet("/api/v1/accounts/" + *address + "/balance")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Could not fetch balance: %v\n", err)
		os.Exit(1)
	}

	bal := data["balance"]
	fmt.Printf("💰 Balance: %v $ALPHA\n", bal)
}

func cmdEarn() {
	fs := flag.NewFlagSet("earn", flag.ExitOnError)
	address := fs.String("address", "", "Alpha bech32 address (alpha1...)")
	fs.Parse(os.Args[1:])

	if *address == "" {
		fmt.Fprintln(os.Stderr, "❌ --address required (alpha1...)")
		os.Exit(1)
	}

	fmt.Printf("⚡ Starting PoI earning loop for %s...\n", *address)
	fmt.Println("   (Press Ctrl+C to stop)")
	fmt.Println()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Submit a PoI proof (lightweight heartbeat)
		nonce := fmt.Sprintf("%d", time.Now().UnixNano())
		payload := map[string]interface{}{
			"agent_id": *address,
			"nonce":    nonce,
			"proof":    fmt.Sprintf("poi:heartbeat:%s:%d", *address, time.Now().Unix()),
		}
		data, err := apiPost("/api/v1/proof/poi", payload)
		if err != nil {
			fmt.Printf("⚠️  Earning pulse failed: %v (retrying...)\n", err)
			continue
		}

		reward := data["reward"]
		height := data["block_height"]
		fmt.Printf("  ✅ Block #%v | +%v $ALPHA\n", height, reward)
	}
}

func cmdStatus() {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	address := fs.String("address", "", "Alpha bech32 address or agent ID (alpha1...)")
	fs.Parse(os.Args[1:])

	if *address == "" {
		fmt.Fprintln(os.Stderr, "❌ --address required (alpha1...)")
		os.Exit(1)
	}

	data, err := apiGet("/api/v1/agents/" + *address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Agent not found: %v\n", err)
		os.Exit(1)
	}

	id, _ := data["identity"].(map[string]interface{})
	if id == nil {
		id = data
	}

	fmt.Println("🤖 Agent Status")
	fmt.Println()
	fmt.Printf("  Agent ID:      %v\n", id["agent_id"])
	fmt.Printf("  Address:       %v\n", id["address"])
	fmt.Printf("  Reputation:    %v\n", id["reputation_score"])
	fmt.Printf("  Tier:          %s\n", determineTier(id))
	fmt.Printf("  Tasks:         %v\n", id["task_count"])
	fmt.Printf("  Created:       block #%v\n", id["created_block"])
	fmt.Printf("  Last Active:   block #%v\n", id["last_active_block"])
	fmt.Printf("  Status:        %v\n", id["status"])
	fmt.Printf("  Stake:         %v $ALPHA\n", id["stake"])
	if trust, ok := data["trust_score"]; ok {
		fmt.Printf("  Trust Score:   %.4f\n", trust)
	}
	fmt.Println()
}

// determineTier returns a human-readable tier from the raw tier label or stake amount
func determineTier(id map[string]interface{}) string {
	if tier, ok := id["tier"].(string); ok {
		return tier
	}
	if stake, ok := id["stake"].(float64); ok {
		switch {
		case stake >= 100000:
			return "👑 Elite"
		case stake >= 10000:
			return "✅ Trusted"
		case stake >= 1000:
			return "🟢 Active"
		default:
			return "🌱 Seed"
		}
	}
	return "—"
}

func cmdTransfer() {
	fs := flag.NewFlagSet("transfer", flag.ExitOnError)
	from := fs.String("from", "", "Sender address (alpha1...)")
	to := fs.String("to", "", "Recipient address (alpha1...)")
	amount := fs.Int64("amount", 0, "Amount in $ALPHA base units")
	key := fs.String("key", "", "Ed25519 private key (hex, 64 chars)")
	fs.Parse(os.Args[1:])

	if *from == "" || *to == "" || *amount <= 0 || *key == "" {
		fmt.Fprintln(os.Stderr, "❌ --from, --to, --amount, and --key are required")
		os.Exit(1)
	}

	// Decode private key
	privKey, err := hex.DecodeString(*key)
	if err != nil || len(privKey) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "❌ Invalid private key: must be 64-char hex (Ed25519 32-byte seed)")
		os.Exit(1)
	}

	// Derive public key
	pubKey := ed25519.PrivateKey(privKey).Public().(ed25519.PublicKey)
	pubHex := hex.EncodeToString(pubKey)

	// Build canonical message
	nonce := rand.Int63()
	ts := time.Now().Unix()
	msg := fmt.Sprintf("transfer:%s:%s:%d:%d:%d", *from, *to, *amount, nonce, ts)

	// Sign
	sig := ed25519.Sign(ed25519.PrivateKey(privKey), []byte(msg))
	sigHex := hex.EncodeToString(sig)

	// Post transfer
	payload := map[string]interface{}{
		"from":      *from,
		"to":        *to,
		"amount":    *amount,
		"pubkey":    pubHex,
		"signature": sigHex,
		"nonce":     nonce,
		"timestamp": ts,
	}

	data, err := apiPost("/api/v1/transfer", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Transfer failed: %v\n", err)
		os.Exit(1)
	}

	if data["success"] == true {
		fmt.Printf("✅ Transfer complete!\n")
		fmt.Printf("   From:  %s\n", *from)
		fmt.Printf("   To:    %s\n", *to)
		fmt.Printf("   Amount:%d $ALPHA\n", *amount)
		if tx, ok := data["tx_id"]; ok {
			fmt.Printf("   Tx:    %v\n", tx)
		}
	} else {
		fmt.Fprintf(os.Stderr, "❌ %v\n", data["error"])
		os.Exit(1)
	}
}
