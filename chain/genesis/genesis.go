// Package genesis provides the production genesis configuration for Alpha Network.
// All chain parameters are defined here and written/read as JSON.
package genesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/ledger"
	"github.com/alpha-network/alpha/chain/store"
)

// GenesisConfig holds all canonical chain parameters for Alpha Network.
// This is the single source of truth for chain initialization.
type GenesisConfig struct {
	// Chain identity
	ChainID     string    `json:"chain_id"`
	GenesisTime time.Time `json:"genesis_time"`
	Version     string    `json:"version"`

	// Token supply (in base units; 1 ALPHA = 1e8 base units)
	TotalSupply              int64 `json:"total_supply"`
	TreasuryBlockRewards     int64 `json:"treasury_block_rewards"`
	TreasuryEcosystemBootstrap int64 `json:"treasury_ecosystem_bootstrap"`

	// Validator economics
	MinStake      int64   `json:"min_stake"`       // minimum stake to register as validator
	BlockTimeMs   int64   `json:"block_time_ms"`   // target block time in milliseconds
	SlashPenalty  float64 `json:"slash_penalty"`   // fraction slashed for misbehavior (0.10 = 10%)

	// Data marketplace
	DataMarketplaceFeeRate float64 `json:"data_marketplace_fee_rate"` // 0.05 = 5% protocol burn

	// Oracle
	OracleFreeForRegistered bool  `json:"oracle_free_for_registered"` // true: free for registered agents
	OracleExternalBurn      int64 `json:"oracle_external_burn"`       // $ALPHA burned per external query

	// Bootstrap program
	BootstrapBonusAgents int64 `json:"bootstrap_bonus_agents"` // first N agents receive bonus
	BootstrapBonusAmount int64 `json:"bootstrap_bonus_amount"` // bonus amount per agent (base units)

	// Initial accounts (pre-funded at genesis, excluding founder/VC)
	InitialAccounts []GenesisAccount `json:"initial_accounts,omitempty"`
}

// GenesisAccount is a pre-funded account in the genesis block.
type GenesisAccount struct {
	Address core.Address `json:"address"`
	Balance core.Amount  `json:"balance"`
	Label   string       `json:"label,omitempty"` // human-readable description
}

// DefaultGenesis returns the canonical Alpha Network genesis configuration.
// No founder or VC allocation — zero pre-mine, 100% community / protocol.
func DefaultGenesis() *GenesisConfig {
	return &GenesisConfig{
		ChainID:     "alpha-1",
		GenesisTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Version:     "0.3.0",

		// Token supply
		TotalSupply:              1_000_000_000, // 1 billion $ALPHA
		TreasuryBlockRewards:     90_000_000,    // 90M for block reward emissions
		TreasuryEcosystemBootstrap: 10_000_000,  // 10M for ecosystem bootstrap

		// Validator economics
		MinStake:     1_000,  // 1000 $ALPHA minimum stake
		BlockTimeMs:  500,    // 500ms target
		SlashPenalty: 0.10,   // 10% slash

		// Data marketplace
		DataMarketplaceFeeRate: 0.05, // 5% burn

		// Oracle pricing
		OracleFreeForRegistered: true,
		OracleExternalBurn:      10, // 10 $ALPHA per external query

		// Bootstrap program
		BootstrapBonusAgents: 1000,
		BootstrapBonusAmount: 10000, // 10,000 base units = 0.0001 $ALPHA (demo scale)

		// No pre-funded accounts at genesis — zero founder/VC allocation
		InitialAccounts: []GenesisAccount{},
	}
}

// WriteGenesisFile writes a genesis configuration to a JSON file.
// Creates parent directories if needed.
func WriteGenesisFile(path string, config *GenesisConfig) error {
	if config == nil {
		return errors.New("config cannot be nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create genesis dir: %w", err)
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal genesis: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ReadGenesisFile reads and parses a genesis config from a JSON file.
func ReadGenesisFile(path string) (*GenesisConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read genesis file: %w", err)
	}
	var config GenesisConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse genesis file: %w", err)
	}
	return &config, nil
}

// InitChainFromGenesis initializes the chain state from the genesis config.
// This must only be called once per chain (on first run when no genesis block exists).
// It:
//   1. Seeds the protocol treasury accounts in the ledger
//   2. Funds any pre-configured InitialAccounts
//   3. Writes the genesis block (height 0) to the store
func InitChainFromGenesis(config *GenesisConfig, st *store.Store, l *ledger.Ledger) error {
	if config == nil {
		return errors.New("genesis config cannot be nil")
	}
	if st == nil {
		return errors.New("store cannot be nil")
	}

	// Guard: do not re-initialize if genesis block already exists
	if st.HasGenesisBlock() {
		return errors.New("chain already initialized (genesis block exists)")
	}

	// Validate config
	if config.TotalSupply <= 0 {
		return errors.New("total_supply must be positive")
	}
	if config.TreasuryBlockRewards+config.TreasuryEcosystemBootstrap > config.TotalSupply {
		return errors.New("treasury allocations exceed total supply")
	}

	// Credit the block rewards treasury
	blockRewardsTreasury := core.Address("alpha1_block_rewards_treasury")
	if l != nil && config.TreasuryBlockRewards > 0 {
		if err := l.Credit(blockRewardsTreasury, core.Amount(config.TreasuryBlockRewards)); err != nil {
			return fmt.Errorf("credit block rewards treasury: %w", err)
		}
	}

	// Credit the ecosystem bootstrap treasury
	ecosystemTreasury := core.Address("alpha1_ecosystem_bootstrap_treasury")
	if l != nil && config.TreasuryEcosystemBootstrap > 0 {
		if err := l.Credit(ecosystemTreasury, core.Amount(config.TreasuryEcosystemBootstrap)); err != nil {
			return fmt.Errorf("credit ecosystem treasury: %w", err)
		}
	}

	// Fund initial accounts (e.g., test/ecosystem accounts; no founder/VC)
	for _, acc := range config.InitialAccounts {
		if l != nil && acc.Balance > 0 {
			if err := l.Credit(acc.Address, acc.Balance); err != nil {
				return fmt.Errorf("credit initial account %s: %w", acc.Address, err)
			}
		}
	}

	// Build the genesis block
	genesis := buildGenesisBlock(config)

	// Write genesis block to store
	if err := st.PutBlock(genesis); err != nil {
		return fmt.Errorf("write genesis block: %w", err)
	}

	// Write chain metadata
	if err := st.PutMeta("chain_id", []byte(config.ChainID)); err != nil {
		return fmt.Errorf("write chain_id meta: %w", err)
	}
	if err := st.PutMeta("genesis_hash", []byte(genesis.Hash)); err != nil {
		return fmt.Errorf("write genesis_hash meta: %w", err)
	}

	return nil
}

// buildGenesisBlock constructs the canonical genesis block from a config.
func buildGenesisBlock(config *GenesisConfig) *core.Block {
	genesis := &core.Block{
		Height:       0,
		Timestamp:    config.GenesisTime.UnixMilli(),
		PrevHash:     "0000000000000000000000000000000000000000000000000000000000000000",
		Transactions: buildGenesisTransactions(config),
		ValidatorID:  core.AgentID("genesis"),
		PoIProof:     nil,
	}
	genesis.ComputeHash()
	return genesis
}

// buildGenesisTransactions creates the initial coinbase-style transactions
// that fund the protocol treasuries and any initial accounts.
func buildGenesisTransactions(config *GenesisConfig) []*core.Transaction {
	txs := make([]*core.Transaction, 0)
	ts := config.GenesisTime.UnixMilli()

	// Block rewards treasury funding transaction
	if config.TreasuryBlockRewards > 0 {
		txs = append(txs, &core.Transaction{
			TxID:      "genesis_block_rewards_treasury",
			Type:      core.TxTransfer,
			From:      core.Address("genesis"),
			To:        core.Address("alpha1_block_rewards_treasury"),
			Amount:    core.Amount(config.TreasuryBlockRewards),
			Memo:      "genesis: block rewards treasury",
			Timestamp: ts,
		})
	}

	// Ecosystem bootstrap treasury
	if config.TreasuryEcosystemBootstrap > 0 {
		txs = append(txs, &core.Transaction{
			TxID:      "genesis_ecosystem_bootstrap_treasury",
			Type:      core.TxTransfer,
			From:      core.Address("genesis"),
			To:        core.Address("alpha1_ecosystem_bootstrap_treasury"),
			Amount:    core.Amount(config.TreasuryEcosystemBootstrap),
			Memo:      "genesis: ecosystem bootstrap treasury",
			Timestamp: ts,
		})
	}

	// Initial accounts
	for i, acc := range config.InitialAccounts {
		txs = append(txs, &core.Transaction{
			TxID:      fmt.Sprintf("genesis_initial_account_%d", i),
			Type:      core.TxTransfer,
			From:      core.Address("genesis"),
			To:        acc.Address,
			Amount:    acc.Balance,
			Memo:      fmt.Sprintf("genesis: %s", acc.Label),
			Timestamp: ts,
		})
	}

	return txs
}
