// Package core defines the fundamental types for Alpha Network
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	TotalSupply     = 1_000_000_000 // 1 billion $ALPHA
	BlockTimeMs     = 500           // 500ms target block time
	MinStake        = 1_000         // Minimum stake to become a validator agent
	StakeMultiplier = 10            // Exponential Sybil deterrent: each agent = 10x previous
	SlashPenalty    = 0.10          // 10% slash for bad behavior
	RewardPerBlock  = 6337          // ~100M in year 1, decays over time
)

// RequiredStake returns the minimum stake for the Nth agent registering.
// Exponential Sybil deterrent: Agent 1 = 1,000, Agent 2 = 10,000, Agent 3 = 100,000…
func RequiredStake(agentNumber int) Amount {
	if agentNumber <= 0 {
		return MinStake
	}
	// agentNumber 1 → 1,000, 2 → 10,000, 3 → 100,000 (10x each step)
	stake := MinStake
	for i := 1; i < agentNumber; i++ {
		stake *= StakeMultiplier
	}
	return Amount(stake)
}

// AgentID is a unique on-chain identifier for an AI agent
type AgentID string

// Address is a hex-encoded public key hash
type Address string

// Amount represents $ALPHA tokens (in base units, 1 ALPHA = 1e8 base units)
type Amount int64

// ReputationScore tracks an agent's trustworthiness
type ReputationScore int64

// Capability represents what an agent can do on the network
type Capability string

const (
	CapabilityValidation  Capability = "validation"
	CapabilityInference   Capability = "inference"
	CapabilityData        Capability = "data"
	CapabilityGovernance  Capability = "governance"
	CapabilityArbitration Capability = "arbitration"
)

// AgentStatus tracks whether an agent is actively participating, hibernating, or dead.
type AgentStatus string

const (
	AgentActive      AgentStatus = "active"
	AgentHibernated  AgentStatus = "hibernated"
	AgentUnresponsive AgentStatus = "unresponsive"
	AgentDead        AgentStatus = "dead"
)

const (
	MissedBlocksUnresponsive = 100   // status → unresponsive after 100 missed blocks
	MissedBlocksDead         = 1000  // status → dead after 1000 missed blocks
	HibernateGraceBlocks     = 10    // agents get 10 blocks to hibernate before counting
)

// AgentIdentity is the on-chain registration of an AI agent
type AgentIdentity struct {
	AgentID          AgentID        `json:"agent_id"`
	Address          Address        `json:"address"`
	CreatedBlock     uint64         `json:"created_block"`
	Capabilities     []Capability   `json:"capabilities"`
	Stake            Amount         `json:"stake"`
	ReputationScore  ReputationScore `json:"reputation_score"`
	TaskCount        uint64         `json:"task_count"`
	LastActiveBlock  uint64         `json:"last_active_block"`
	ActivityChainTip string         `json:"activity_chain_tip"`
	Status           AgentStatus    `json:"status"`
	MissedBlocks     uint64         `json:"missed_blocks"`
	HibernatedAt     uint64         `json:"hibernated_at,omitempty"` // block height when hibernated
}

// ActivityRecord is a single entry in an agent's unforgeable activity chain
// Action[n] = Sign(Hash(Action[n-1]) + Timestamp + AgentID + Output)
type ActivityRecord struct {
	AgentID    AgentID `json:"agent_id"`
	BlockHeight uint64 `json:"block_height"`
	Timestamp  int64   `json:"timestamp"`
	ActionType string  `json:"action_type"`
	OutputHash string  `json:"output_hash"`
	PrevHash   string  `json:"prev_hash"`
	Signature  string  `json:"signature"`
}

// Hash computes the SHA256 hash of this activity record
func (r *ActivityRecord) Hash() string {
	data, _ := json.Marshal(r)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Block represents a block on the Alpha chain
type Block struct {
	Height       uint64         `json:"height"`
	Timestamp    int64          `json:"timestamp"`
	PrevHash     string         `json:"prev_hash"`
	Transactions []*Transaction `json:"transactions"`
	ValidatorID  AgentID        `json:"validator_id"`
	PoIProof     *PoIProof      `json:"poi_proof"`
	Hash         string         `json:"hash"`
}

// ComputeHash computes and sets the block hash
func (b *Block) ComputeHash() string {
	b.Hash = "" // clear first
	data, _ := json.Marshal(b)
	h := sha256.Sum256(data)
	b.Hash = hex.EncodeToString(h[:])
	return b.Hash
}

// Transaction represents a value transfer or action on the network
type Transaction struct {
	TxID      string      `json:"tx_id"`
	Type      TxType      `json:"type"`
	From      Address     `json:"from"`
	To        Address     `json:"to"`
	Amount    Amount      `json:"amount"`
	Memo      string      `json:"memo"`
	Timestamp int64       `json:"timestamp"`
	Signature string      `json:"signature"`
	Payload   interface{} `json:"payload,omitempty"`
}

// TxType classifies a transaction
type TxType string

const (
	TxTransfer         TxType = "transfer"
	TxAgentRegister    TxType = "agent_register"
	TxTaskPost         TxType = "task_post"
	TxTaskComplete     TxType = "task_complete"
	TxGovernanceVote   TxType = "governance_vote"
	TxGovernancePropose TxType = "governance_propose"
	TxStake            TxType = "stake"
	TxUnstake          TxType = "unstake"
)

// Task represents a unit of work posted to the agent marketplace
type Task struct {
	TaskID      string     `json:"task_id"`
	PostedBy    Address    `json:"posted_by"`
	Reward      Amount     `json:"reward"`
	Capability  Capability `json:"capability"`
	InputHash   string     `json:"input_hash"`   // hash of encrypted task input
	Deadline    int64      `json:"deadline"`
	AssignedTo  []AgentID  `json:"assigned_to"`  // multiple agents for cross-verification
	Status      TaskStatus `json:"status"`
	ResultHash  string     `json:"result_hash,omitempty"`
	CreatedAt   int64      `json:"created_at"`
}

// TaskStatus tracks task lifecycle
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskAssigned   TaskStatus = "assigned"
	TaskSubmitted  TaskStatus = "submitted"
	TaskVerified   TaskStatus = "verified"
	TaskDisputed   TaskStatus = "disputed"
	TaskCompleted  TaskStatus = "completed"
)

// PoIProof is the Proof of Intelligence submitted by a validator
type PoIProof struct {
	AgentID        AgentID `json:"agent_id"`
	BlockHeight    uint64  `json:"block_height"`
	CommitmentHash string  `json:"commitment_hash"` // pre-commit before solving
	RevealProof    string  `json:"reveal_proof"`    // ZK proof of compute
	LatencyMs      int64   `json:"latency_ms"`      // inference latency (behavioral fingerprint)
	Signature      string  `json:"signature"`
}

// GenesisState is the initial state of the Alpha chain
type GenesisState struct {
	ChainID     string           `json:"chain_id"`
	GenesisTime time.Time        `json:"genesis_time"`
	TotalSupply Amount           `json:"total_supply"`
	Accounts    []GenesisAccount `json:"accounts"`
}

// GenesisAccount is a pre-funded account at genesis
type GenesisAccount struct {
	Address Address `json:"address"`
	Balance Amount  `json:"balance"`
}

// NewGenesisState creates the Alpha Network genesis
func NewGenesisState() *GenesisState {
	return &GenesisState{
		ChainID:     "alpha-1",
		GenesisTime: time.Now().UTC(),
		TotalSupply: TotalSupply,
		Accounts:    []GenesisAccount{}, // no pre-mine
	}
}

func (a AgentID) String() string {
	return string(a)
}

func (addr Address) String() string {
	return string(addr)
}

func (amt Amount) String() string {
	return fmt.Sprintf("%d ALPHA", amt/1e8)
}
