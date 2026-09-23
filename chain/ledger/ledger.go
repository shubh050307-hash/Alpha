// Package ledger implements the $ALPHA token account ledger
// Thread-safe, with overdraft protection, atomic transfers, deflationary burn tracking,
// and persistent balance storage via configurable callbacks.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/alpha-network/alpha/chain/core"
)

// TxRecord is an immutable ledger entry for the transaction log
type TxRecord struct {
	TxID      string       `json:"tx_id"`
	Type      string       `json:"type"`
	From      core.Address `json:"from"`
	To        core.Address `json:"to"`
	Amount    core.Amount  `json:"amount"`
	Memo      string       `json:"memo"`
	Timestamp int64        `json:"timestamp"`
}

// BalancePersister is called after every balance change to persist an account's balance.
// addr is the account address, amount is the new balance.
type BalancePersister func(addr core.Address, amount core.Amount) error

// MetaPersister is called after burns to persist ledger metadata (burned total, supply).
type MetaPersister func(key string, value []byte) error

// Ledger manages all $ALPHA account balances and burn tracking
type Ledger struct {
	mu              sync.RWMutex
	balances        map[core.Address]core.Amount
	nonces          map[core.Address]int64 // per-address nonce for replay protection
	txLog           []*TxRecord
	totalBurned     core.Amount
	totalSupply     core.Amount
	persistBalance  BalancePersister // optional callback for persisting balances
	persistMeta     MetaPersister    // optional callback for persisting metadata
}

// NewLedger creates a Ledger with a fixed total supply
func NewLedger(totalSupply core.Amount) *Ledger {
	return &Ledger{
		balances:    make(map[core.Address]core.Amount),
		nonces:      make(map[core.Address]int64),
		txLog:       make([]*TxRecord, 0, 1024),
		totalSupply: totalSupply,
	}
}

// SetPersisters wires optional callbacks for persistent storage.
// balanceFn is called after every Credit/Debit/Transfer/BurnSupply.
// metaFn is called after every BurnFromProtocol / BurnSupply to save burned/supply totals.
func (l *Ledger) SetPersisters(balanceFn BalancePersister, metaFn MetaPersister) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.persistBalance = balanceFn
	l.persistMeta = metaFn
}

// SetBalancePersister wires only the balance persistence callback (meta is a no-op if unset).
func (l *Ledger) SetBalancePersister(fn BalancePersister) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.persistBalance = fn
}

// SnapshotBalances returns a copy of all account balances.
// Used to create periodic snapshots without holding the lock for I/O.
func (l *Ledger) SnapshotBalances() map[core.Address]core.Amount {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[core.Address]core.Amount, len(l.balances))
	for k, v := range l.balances {
		out[k] = v
	}
	return out
}

// Credit adds $ALPHA to an address (e.g., block rewards, genesis funding)
// Returns an error only if amount is non-positive.
func (l *Ledger) Credit(addr core.Address, amount core.Amount) error {
	if amount <= 0 {
		return errors.New("credit amount must be positive")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.balances[addr] += amount

	l.recordTx(&TxRecord{
		TxID:      l.genTxID("credit", string(addr)),
		Type:      "credit",
		To:        addr,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
	})

	l.saveBalance(addr)
	return nil
}

// Debit removes $ALPHA from an address with overdraft protection
func (l *Ledger) Debit(addr core.Address, amount core.Amount) error {
	if amount <= 0 {
		return errors.New("debit amount must be positive")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	bal := l.balances[addr]
	if bal < amount {
		return fmt.Errorf("insufficient balance: have %d, need %d", bal, amount)
	}
	l.balances[addr] -= amount

	l.recordTx(&TxRecord{
		TxID:      l.genTxID("debit", string(addr)),
		Type:      "debit",
		From:      addr,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
	})

	l.saveBalance(addr)
	return nil
}

// InternalTransfer moves $ALPHA between two addresses WITHOUT nonce validation.
// Used by the block producer when processing mempool transactions (nonce already
// validated at submission time) and by the intelligence marketplace for data purchases.
func (l *Ledger) InternalTransfer(from, to core.Address, amount core.Amount, memo string) (string, error) {
	if amount <= 0 {
		return "", errors.New("transfer amount must be positive")
	}
	if from == to {
		return "", errors.New("from and to addresses must differ")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	bal := l.balances[from]
	if bal < amount {
		return "", fmt.Errorf("insufficient balance")
	}

	l.balances[from] -= amount
	l.balances[to] += amount

	txID := l.genTxID("internal_transfer", string(from)+string(to))
	l.recordTx(&TxRecord{
		TxID:      txID,
		Type:      "internal_transfer",
		From:      from,
		To:        to,
		Amount:    amount,
		Memo:      memo,
		Timestamp: time.Now().Unix(),
	})

	l.saveBalance(from)
	l.saveBalance(to)
	return txID, nil
}

// Transfer atomically moves $ALPHA from one address to another.
// Returns the TxID or an error (overdraft / invalid amounts / nonce violation).
// nonce is the sender's per-address nonce for replay protection; must be
// exactly one greater than the last used nonce (or 0 for first transfer).
func (l *Ledger) Transfer(from, to core.Address, amount core.Amount, memo string, nonce int64) (string, error) {
	if amount <= 0 {
		return "", errors.New("transfer amount must be positive")
	}
	if from == to {
		return "", errors.New("from and to addresses must differ")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Per-address nonce validation — prevents replay attacks
	if err := l.validateNonce(from, nonce); err != nil {
		return "", err
	}

	bal := l.balances[from]
	if bal < amount {
		return "", fmt.Errorf("insufficient balance: have %d, need %d", bal, amount)
	}

	l.balances[from] -= amount
	l.balances[to] += amount

	txID := l.genTxID("transfer", string(from)+string(to))
	l.recordTx(&TxRecord{
		TxID:      txID,
		Type:      "transfer",
		From:      from,
		To:        to,
		Amount:    amount,
		Memo:      memo,
		Timestamp: time.Now().Unix(),
	})

	l.nonces[from] = nonce // record the consumed nonce
	l.saveBalance(from)
	l.saveBalance(to)
	return txID, nil
}

// Balance returns the current balance for an address (0 if unknown)
func (l *Ledger) Balance(addr core.Address) core.Amount {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.balances[addr]
}

// BurnSupply permanently removes tokens from circulation (deflationary mechanism)
// Tokens are deducted from the given address and tracked as burned
func (l *Ledger) BurnSupply(addr core.Address, amount core.Amount) error {
	if amount <= 0 {
		return errors.New("burn amount must be positive")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	bal := l.balances[addr]
	if bal < amount {
		return fmt.Errorf("insufficient balance to burn: have %d, need %d", bal, amount)
	}

	l.balances[addr] -= amount
	l.totalBurned += amount
	l.totalSupply -= amount // consistent with BurnFromProtocol — burn reduces total supply

	l.recordTx(&TxRecord{
		TxID:      l.genTxID("burn", string(addr)),
		Type:      "burn",
		From:      addr,
		Amount:    amount,
		Memo:      "protocol burn",
		Timestamp: time.Now().Unix(),
	})

	l.saveBalance(addr)
	l.saveMeta()
	return nil
}

// BurnFromProtocol burns tokens from the protocol treasury (no address needed)
// Used for fee burns that originate from the protocol itself.
// Semantically consistent with BurnSupply: both reduce totalSupply + increment totalBurned.
func (l *Ledger) BurnFromProtocol(amount core.Amount) error {
	if amount <= 0 {
		return errors.New("burn amount must be positive")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.totalBurned += amount
	l.totalSupply -= amount

	l.recordTx(&TxRecord{
		TxID:      l.genTxID("protocol_burn", "protocol"),
		Type:      "protocol_burn",
		Amount:    amount,
		Memo:      "protocol fee burn",
		Timestamp: time.Now().Unix(),
	})

	l.saveMeta()
	return nil
}

// CirculatingSupply returns total supply minus burned tokens
func (l *Ledger) CirculatingSupply() core.Amount {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.totalSupply - l.totalBurned
}

// TotalBurned returns the cumulative burned token count
func (l *Ledger) TotalBurned() core.Amount {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.totalBurned
}

// TotalSupply returns the current total supply (may decrease due to burns)
func (l *Ledger) TotalSupply() core.Amount {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.totalSupply
}

// TxHistory returns the last n transaction records (all if n <= 0)
func (l *Ledger) TxHistory(n int) []*TxRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n <= 0 || n >= len(l.txLog) {
		out := make([]*TxRecord, len(l.txLog))
		copy(out, l.txLog)
		return out
	}
	start := len(l.txLog) - n
	out := make([]*TxRecord, n)
	copy(out, l.txLog[start:])
	return out
}

// AddressHistory returns transactions involving a given address
func (l *Ledger) AddressHistory(addr core.Address, limit int) []*TxRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*TxRecord, 0)
	for _, tx := range l.txLog {
		if tx.From == addr || tx.To == addr {
			result = append(result, tx)
		}
	}
	if limit > 0 && limit < len(result) {
		return result[len(result)-limit:]
	}
	return result
}

// Stats returns a summary of ledger state
func (l *Ledger) Stats() map[string]interface{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return map[string]interface{}{
		"total_supply":       l.totalSupply,
		"total_burned":       l.totalBurned,
		"circulating_supply": l.totalSupply - l.totalBurned,
		"account_count":      len(l.balances),
		"tx_count":           len(l.txLog),
	}
}

// ForEachBalance calls fn for every account and its balance.
// This can be used to bulk-persist all balances at startup or shutdown.
func (l *Ledger) ForEachBalance(fn func(addr core.Address, amount core.Amount) error) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for addr, amount := range l.balances {
		if err := fn(addr, amount); err != nil {
			return err
		}
	}
	return nil
}

// LoadBalances bulk-loads account balances from a pre-scanned map.
// Existing balances in the ledger are overwritten by the loaded values.
// This is used at startup to restore state from BadgerDB.
func (l *Ledger) LoadBalances(entries map[core.Address]core.Amount) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	loaded := 0
	for addr, amount := range entries {
		l.balances[addr] = amount
		loaded++
	}
	if loaded > 0 {
		log.Printf("💾 Ledger: loaded %d account balances from persistent store", loaded)
	}
	return nil
}

// SetTotalBurned restores the burned token count from persistent storage.
func (l *Ledger) SetTotalBurned(amount core.Amount) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.totalBurned = amount
}

// SetTotalSupply restores the total supply from persistent storage.
func (l *Ledger) SetTotalSupply(amount core.Amount) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.totalSupply = amount
}

// --- internal helpers ---

// validateNonce checks that the provided nonce is exactly lastUsed+1 (or 0 for first tx).
// Caller must hold l.mu Lock.
func (l *Ledger) validateNonce(addr core.Address, nonce int64) error {
	lastUsed, exists := l.nonces[addr]
	if !exists {
		// First transaction for this address — nonce must be 0
		if nonce != 0 {
			return fmt.Errorf("invalid nonce: expected 0 for first transfer, got %d", nonce)
		}
		return nil
	}
	expected := lastUsed + 1
	if nonce != expected {
		return fmt.Errorf("invalid nonce: expected %d, got %d", expected, nonce)
	}
	return nil
}

// Nonce returns the last used nonce for an address (0 if never seen).
func (l *Ledger) Nonce(addr core.Address) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.nonces[addr]
}

func (l *Ledger) recordTx(tx *TxRecord) {
	l.txLog = append(l.txLog, tx)
}

func (l *Ledger) genTxID(txType, seed string) string {
	raw := fmt.Sprintf("%s:%s:%d", txType, seed, time.Now().UnixNano())
	h := sha256.Sum256([]byte(raw))
	return "tx_" + hex.EncodeToString(h[:])[:24]
}

// saveBalance calls the persistence callback (best-effort).
// Caller must hold l.mu Lock.
func (l *Ledger) saveBalance(addr core.Address) {
	if l.persistBalance == nil {
		return
	}
	if err := l.persistBalance(addr, l.balances[addr]); err != nil {
		log.Printf("⚠️  Ledger: persist balance for %s: %v", addr, err)
	}
}

// saveMeta calls the metadata persistence callback (best-effort).
// Caller must hold l.mu Lock.
func (l *Ledger) saveMeta() {
	if l.persistMeta == nil {
		return
	}
	burned := fmt.Sprintf("%d", l.totalBurned)
	supply := fmt.Sprintf("%d", l.totalSupply)
	if err := l.persistMeta("total_burned", []byte(burned)); err != nil {
		log.Printf("⚠️  Ledger: persist meta total_burned: %v", err)
	}
	if err := l.persistMeta("total_supply", []byte(supply)); err != nil {
		log.Printf("⚠️  Ledger: persist meta total_supply: %v", err)
	}
}
