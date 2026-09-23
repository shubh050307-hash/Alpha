// Alpha Network Testnet Faucet
//
// Serves a simple web page where developers can request testnet $ALPHA.
// Rate-limited, anti-bot, and backed by a faucet treasury account.
//
// Usage:
//   go run ./cmd/faucet -node http://localhost:8080 -port 8085 -treasury-key <hex>
//
// Or compiled:
//   alphanode faucet -node http://localhost:8080 -port 8085

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	faucetAmount       = 5000 // $ALPHA per request (enough for first agent stake)
	cooldownMinutes    = 60   // minutes between requests per IP
	maxRequestsPerDay  = 10   // max per IP per day
	dropSize           = 5000
)

// FaucetServer serves the testnet faucet
type FaucetServer struct {
	nodeURL    string
	port       int
	treasurySK ed25519.PrivateKey
	treasuryPK ed25519.PublicKey
	treasuryAddr string

	mu          sync.Mutex
	ipRequests  map[string]*requestLog // IP → request history
}

type requestLog struct {
	firstRequest time.Time
	lastRequest  time.Time
	count        int // today's count
}

func main() {
	nodeURL := flag.String("node", "http://localhost:8080", "Alpha node API URL")
	port := flag.Int("port", 8085, "Faucet listen port")
	treasuryKey := flag.String("treasury-key", "", "Treasury Ed25519 private key (hex, 64 chars)")
	flag.Parse()

	// Derive treasury keypair
	var sk ed25519.PrivateKey
	var pk ed25519.PublicKey

	if *treasuryKey != "" {
		keyBytes, err := hex.DecodeString(*treasuryKey)
		if err != nil || len(keyBytes) != ed25519.SeedSize {
			log.Fatalf("Invalid treasury-key: must be 64-char hex (32 bytes seed): %v", err)
		}
		sk = ed25519.NewKeyFromSeed(keyBytes)
		pk = sk.Public().(ed25519.PublicKey)
	} else {
		// Generate a fresh keypair (for testing)
		pk, sk, _ = ed25519.GenerateKey(rand.Reader)
		log.Printf("⚠️  No treasury key provided — generated fresh keypair.")
		log.Printf("   Fund this address first: %s", addressFromPubkey(pk))
		log.Printf("   Pass -treasury-key %x for persistence", sk.Seed())
	}

	fs := &FaucetServer{
		nodeURL:    strings.TrimRight(*nodeURL, "/"),
		port:       *port,
		treasurySK: sk,
		treasuryPK: pk,
		treasuryAddr: addressFromPubkey(pk),
		ipRequests: make(map[string]*requestLog),
	}

	// Periodically clean old request logs
	go func() {
		for range time.Tick(30 * time.Minute) {
			fs.cleanOldLogs()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", fs.handlePage)
	mux.HandleFunc("/api/faucet/send", fs.handleSend)
	mux.HandleFunc("/api/faucet/stats", fs.handleStats)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("🚰 Alpha Network Faucet listening on %s", addr)
	log.Printf("   Treasury: %s (balance check on first request)", fs.treasuryAddr)
	log.Printf("   Drop: %d $ALPHA | Cooldown: %d min | Cap: %d/day per IP", dropSize, cooldownMinutes, maxRequestsPerDay)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// addressFromPubkey derives the Alpha bech32-style address from an Ed25519 pubkey.
func addressFromPubkey(pk ed25519.PublicKey) string {
	return "alpha1" + hex.EncodeToString(pk)[:40]
}

// ── Page handler ──────────────────────────────────────────────────────

func (fs *FaucetServer) handlePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(faucetHTML))
}

// ── Send handler ──────────────────────────────────────────────────────

func (fs *FaucetServer) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]interface{}{"error": "POST required"})
		return
	}

	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": "invalid JSON"})
		return
	}

	// Validate address format
	addr := strings.TrimSpace(req.Address)
	if addr == "" {
		writeJSON(w, 400, map[string]interface{}{"error": "address required"})
		return
	}
	if !strings.HasPrefix(addr, "alpha1") || len(addr) < 10 {
		writeJSON(w, 400, map[string]interface{}{"error": "invalid Alpha address: must start with alpha1"})
		return
	}

	// Rate limiting by IP
	ip := fs.clientIP(r)
	fs.mu.Lock()
	rl, ok := fs.ipRequests[ip]
	now := time.Now()
	if !ok {
		rl = &requestLog{firstRequest: now}
		fs.ipRequests[ip] = rl
	}

	// Check cooldown
	if !rl.lastRequest.IsZero() && now.Sub(rl.lastRequest) < cooldownMinutes*time.Minute {
		remaining := cooldownMinutes*time.Minute - now.Sub(rl.lastRequest)
		fs.mu.Unlock()
		writeJSON(w, 429, map[string]interface{}{
			"error":             "rate limited — cooldown active",
			"retry_after_secs":  int(remaining.Seconds()),
			"cooldown_minutes":  cooldownMinutes,
		})
		return
	}

	// Check daily cap
	if !isSameDay(rl.firstRequest, now) {
		rl.firstRequest = now
		rl.count = 0
	}
	if rl.count >= maxRequestsPerDay {
		fs.mu.Unlock()
		writeJSON(w, 429, map[string]interface{}{
			"error":       "daily limit reached",
			"max_per_day": maxRequestsPerDay,
		})
		return
	}

	rl.count++
	rl.lastRequest = now
	fs.mu.Unlock()

	// Build signed transfer
	nonce := time.Now().UnixNano()
	ts := time.Now().Unix()
	message := fmt.Sprintf("transfer:%s:%s:%d:%d:%d", fs.treasuryAddr, addr, faucetAmount, nonce, ts)
	sig := ed25519.Sign(fs.treasurySK, []byte(message))

	transferReq := map[string]interface{}{
		"from":      fs.treasuryAddr,
		"to":        addr,
		"amount":    faucetAmount,
		"memo":      "testnet faucet drop",
		"pubkey":    hex.EncodeToString(fs.treasuryPK),
		"signature": hex.EncodeToString(sig),
		"nonce":     nonce,
		"timestamp": ts,
	}

	body, _ := json.Marshal(transferReq)
	resp, err := http.Post(fs.nodeURL+"/api/v1/transfer", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("❌ Faucet: node unreachable: %v", err)
		writeJSON(w, 502, map[string]interface{}{"error": "node unreachable — try again later"})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if resp.StatusCode >= 400 {
		errMsg := "unknown error"
		if e, ok := result["error"]; ok {
			errMsg = fmt.Sprintf("%v", e)
		}
		log.Printf("❌ Faucet: transfer rejected: %s", errMsg)
		writeJSON(w, 502, map[string]interface{}{
			"error":  "transfer failed — faucet may be empty",
			"detail": errMsg,
		})
		return
	}

	txID := ""
	if id, ok := result["tx_id"]; ok {
		txID = fmt.Sprintf("%v", id)
	}

	log.Printf("🚰 Faucet drop: %d $ALPHA → %s (tx: %s, ip: %s)", faucetAmount, addr[:20], txID, ip)
	writeJSON(w, 200, map[string]interface{}{
		"success":    true,
		"tx_id":      txID,
		"amount":     faucetAmount,
		"to":         addr,
		"token":      "$ALPHA",
		"network":    "testnet",
		"message":    fmt.Sprintf("Sent %d testnet $ALPHA! Use this to register an agent (stake: 1000 $ALPHA).", faucetAmount),
		"next_steps": []string{
			"Register an agent: POST /api/v1/agents/register",
			"Check your balance: GET /api/v1/accounts/" + addr + "/balance",
			"View the explorer: http://localhost:8082",
		},
	})
}

// ── Stats handler ─────────────────────────────────────────────────────

func (fs *FaucetServer) handleStats(w http.ResponseWriter, r *http.Request) {
	fs.mu.Lock()
	totalToday := 0
	for _, rl := range fs.ipRequests {
		if isSameDay(rl.firstRequest, time.Now()) {
			totalToday += rl.count
		}
	}
	uniqueIPs := len(fs.ipRequests)
	fs.mu.Unlock()

	writeJSON(w, 200, map[string]interface{}{
		"success":         true,
		"treasury_address": fs.treasuryAddr,
		"drop_amount":     dropSize,
		"cooldown_minutes": cooldownMinutes,
		"max_per_day":     maxRequestsPerDay,
		"drops_today":     totalToday,
		"unique_ips":      uniqueIPs,
		"network":         "testnet",
	})
}

// ── Helpers ───────────────────────────────────────────────────────────

func (fs *FaucetServer) clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func (fs *FaucetServer) cleanOldLogs() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	cutoff := time.Now().Add(-48 * time.Hour)
	for ip, rl := range fs.ipRequests {
		if rl.firstRequest.Before(cutoff) && !isSameDay(rl.lastRequest, time.Now()) {
			delete(fs.ipRequests, ip)
		}
	}
}

func isSameDay(a, b time.Time) bool {
	ya, ma, da := a.Date()
	yb, mb, db := b.Date()
	return ya == yb && ma == mb && da == db
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// ── HTML page ─────────────────────────────────────────────────────────

const faucetHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Alpha Network — Testnet Faucet</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #0a0a1a; color: #e0e0f0;
    display: flex; align-items: center; justify-content: center;
    min-height: 100vh; padding: 1rem;
  }
  .card {
    background: #12123a; border: 1px solid #2a2a6a; border-radius: 16px;
    padding: 2rem; max-width: 480px; width: 100%;
    box-shadow: 0 0 40px rgba(80, 80, 255, 0.1);
  }
  h1 { font-size: 1.5rem; color: #8080ff; margin-bottom: 0.25rem; }
  .subtitle { color: #8080aa; font-size: 0.9rem; margin-bottom: 1.5rem; }
  label { display: block; font-size: 0.85rem; color: #a0a0d0; margin-bottom: 0.3rem; }
  input {
    width: 100%; padding: 0.75rem; border-radius: 8px;
    border: 1px solid #3a3a7a; background: #0a0a2a; color: #e0e0f0;
    font-size: 0.95rem; font-family: monospace;
    outline: none; transition: border-color 0.2s;
  }
  input:focus { border-color: #6060ff; }
  button {
    width: 100%; padding: 0.75rem; margin-top: 1rem;
    border: none; border-radius: 8px; background: #4040ff;
    color: white; font-size: 1rem; font-weight: 600;
    cursor: pointer; transition: background 0.2s;
  }
  button:hover { background: #5050ff; }
  button:disabled { background: #2a2a5a; cursor: not-allowed; }
  .result {
    margin-top: 1rem; padding: 1rem; border-radius: 8px;
    font-size: 0.9rem; display: none;
  }
  .result.success { display: block; background: #0a2a1a; border: 1px solid #1a6a2a; color: #60ff80; }
  .result.error { display: block; background: #2a0a0a; border: 1px solid #6a1a1a; color: #ff6060; }
  .info {
    margin-top: 1.5rem; padding-top: 1rem; border-top: 1px solid #2a2a5a;
    font-size: 0.8rem; color: #7070a0;
  }
  .info span { color: #a0a0d0; }
  .logo { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 1rem; }
  .logo-icon { font-size: 1.8rem; }
</style>
</head>
<body>
<div class="card">
  <div class="logo">
    <span class="logo-icon">🔷</span>
    <h1>Alpha Network Faucet</h1>
  </div>
  <p class="subtitle">Get testnet $ALPHA to register your AI agent.</p>

  <form id="faucet-form" onsubmit="requestFaucet(event)">
    <label for="address">Your Alpha address</label>
    <input type="text" id="address" name="address"
           placeholder="alpha1a1b2c3d4e5f6..." required
           autocomplete="off" spellcheck="false">
    <button type="submit" id="submit-btn">🚰 Request 5,000 $ALPHA</button>
  </form>

  <div id="result" class="result"></div>

  <div class="info">
    <p>💧 <span>5,000 testnet $ALPHA</span> per drop</p>
    <p>⏱ <span>60 minute</span> cooldown per IP</p>
    <p>📊 Max <span>10 drops/day</span></p>
    <p>💡 Need <span>1,000 $ALPHA</span> to register an agent</p>
    <p style="margin-top:0.5rem">🔗 <a href="https://alphanetx.xyz" style="color:#8080ff;">alphanetx.xyz</a> &nbsp;|&nbsp; <a href="https://github.com/galaxiaalphanet/Alpha-Network" style="color:#8080ff;">GitHub</a></p>
  </div>
</div>

<script>
async function requestFaucet(e) {
  e.preventDefault();
  const addr = document.getElementById('address').value.trim();
  const btn = document.getElementById('submit-btn');
  const result = document.getElementById('result');

  result.className = 'result';
  result.style.display = 'none';
  btn.disabled = true;
  btn.textContent = '⏳ Sending...';

  try {
    const resp = await fetch('/api/faucet/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address: addr })
    });
    const data = await resp.json();

    if (resp.ok && data.success) {
      result.className = 'result success';
      result.innerHTML = '✅ <b>Sent ' + data.amount + ' testnet $ALPHA!</b><br>' +
        'TX: <code>' + (data.tx_id || 'pending') + '</code><br>' +
        'To verify: <code>GET /api/v1/accounts/' + addr + '/balance</code>';
    } else {
      result.className = 'result error';
      result.textContent = '❌ ' + (data.error || 'Unknown error');
      if (data.retry_after_secs) {
        result.textContent += ' — retry in ' + Math.ceil(data.retry_after_secs/60) + ' min';
      }
    }
  } catch (err) {
    result.className = 'result error';
    result.textContent = '❌ Faucet unreachable — try again';
  } finally {
    btn.disabled = false;
    btn.textContent = '🚰 Request 5,000 $ALPHA';
  }
}
</script>
</body>
</html>`
