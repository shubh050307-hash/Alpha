// Package governance implements on-chain protocol governance for Alpha Network.
//
// Registered agents propose and vote on protocol parameters. Voting power is
// weighted by stake + reputation. Proposals follow a lifecycle:
//
//   pending   → active (voting) → passed / rejected → executed
//
// Quorum: >50% of total stake must vote. Threshold: >66% of votes in favor.
// Voting period: configurable (default 100 blocks ≈ 50 seconds).
package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/ledger"
)

// ─── Types ──────────────────────────────────────────────────────────────────

// ProposalType classifies what kind of change is being proposed.
type ProposalType string

const (
	PropBlockTime     ProposalType = "block_time"
	PropRewardRate    ProposalType = "reward_rate"
	PropSlashPenalty  ProposalType = "slash_penalty"
	PropOracleFee     ProposalType = "oracle_fee"
	PropMinStake      ProposalType = "min_stake"
	PropMarketplaceFee ProposalType = "marketplace_fee"
	PropGeneral       ProposalType = "general"
)

func (pt ProposalType) Valid() bool {
	switch pt {
	case PropBlockTime, PropRewardRate, PropSlashPenalty, PropOracleFee,
		PropMinStake, PropMarketplaceFee, PropGeneral:
		return true
	}
	return false
}

// ProposalStatus tracks the lifecycle of a proposal.
type ProposalStatus string

const (
	PropPending  ProposalStatus = "pending"  // submitted, discussion period
	PropActive   ProposalStatus = "active"   // voting open
	PropPassed   ProposalStatus = "passed"   // quorum met, threshold reached
	PropRejected ProposalStatus = "rejected" // quorum not met or threshold not reached
	PropExecuted ProposalStatus = "executed" // changes applied
)

// Proposal is a governance proposal submitted by an agent.
type Proposal struct {
	ID          string         `json:"id"`
	Type        ProposalType   `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	NewValue    string         `json:"new_value"`   // proposed new parameter value
	ProposerID  core.AgentID   `json:"proposer_id"`
	Status      ProposalStatus `json:"status"`
	CreatedAt   int64          `json:"created_at"`
	VotingStart int64          `json:"voting_start"` // block height when voting opens
	VotingEnd   int64          `json:"voting_end"`   // block height when voting closes
	VotesYes    core.Amount    `json:"votes_yes"`    // total stake voted YES
	VotesNo     core.Amount    `json:"votes_no"`     // total stake voted NO
	ExecutedAt  int64          `json:"executed_at,omitempty"`
}

// Vote represents a single agent's vote on a proposal.
type Vote struct {
	AgentID     core.AgentID `json:"agent_id"`
	ProposalID  string       `json:"proposal_id"`
	Choice      bool         `json:"choice"`       // true = yes, false = no
	VotingPower core.Amount  `json:"voting_power"` // stake + reputation bonus
	BlockHeight uint64       `json:"block_height"`
	Timestamp   int64        `json:"timestamp"`
}

// AgentStakeProvider is implemented by the agent registry to look up stake info.
type AgentStakeProvider interface {
	GetAgent(id core.AgentID) (*core.AgentIdentity, error)
	ListAgents(cap *core.Capability) []*core.AgentIdentity
}

// ─── Module ─────────────────────────────────────────────────────────────────

// Config holds governance parameters.
type Config struct {
	VotingBlocks    int64         // number of blocks voting stays open
	QuorumPercent   float64       // % of total stake that must vote (0.0-1.0)
	ThresholdPercent float64      // % of votes in favor needed (0.0-1.0)
	ProposerMinStake core.Amount  // minimum stake to submit proposals
}

// DefaultConfig returns sensible testnet defaults.
func DefaultConfig() Config {
	return Config{
		VotingBlocks:     100,   // ~50 seconds at 500ms blocks
		QuorumPercent:    0.50,  // >50% of stake must vote
		ThresholdPercent: 0.66,  // >66% of votes in favor
		ProposerMinStake: 5000,
	}
}

// Module manages the governance lifecycle.
type Module struct {
	mu        sync.RWMutex
	proposals map[string]*Proposal
	votes     map[string][]*Vote // proposalID → votes
	config    Config
	ledger    *ledger.Ledger
	registry  AgentStakeProvider
}

// NewModule creates a governance module.
func NewModule(cfg Config, l *ledger.Ledger, registry AgentStakeProvider) *Module {
	if cfg.VotingBlocks == 0 {
		cfg = DefaultConfig()
	}
	return &Module{
		proposals: make(map[string]*Proposal),
		votes:     make(map[string][]*Vote),
		config:    cfg,
		ledger:    l,
		registry:  registry,
	}
}

// ─── Proposal lifecycle ─────────────────────────────────────────────────────

// Propose submits a new governance proposal. Only agents with sufficient stake
// may propose. The proposal enters "pending" state; after the current block,
// voting begins.
func (m *Module) Propose(
	propType ProposalType,
	title, description, newValue string,
	proposerID core.AgentID,
	currentBlock uint64,
) (*Proposal, error) {
	if !propType.Valid() {
		return nil, fmt.Errorf("invalid proposal type: %s", propType)
	}
	if title == "" {
		return nil, errors.New("title required")
	}
	if proposerID == "" {
		return nil, errors.New("proposer agent ID required")
	}

	// Verify proposer has sufficient stake
	if m.registry != nil {
		agent, err := m.registry.GetAgent(proposerID)
		if err != nil {
			return nil, fmt.Errorf("proposer agent not found: %w", err)
		}
		if agent.Stake < m.config.ProposerMinStake {
			return nil, fmt.Errorf("insufficient stake to propose: have %d, need %d",
				agent.Stake, m.config.ProposerMinStake)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prop := &Proposal{
		ID:          genProposalID(proposerID, title),
		Type:        propType,
		Title:       title,
		Description: description,
		NewValue:    newValue,
		ProposerID:  proposerID,
		Status:      PropPending,
		CreatedAt:   time.Now().Unix(),
		VotingStart: int64(currentBlock) + 10, // 10 block discussion period
		VotingEnd:   int64(currentBlock) + 10 + m.config.VotingBlocks,
	}

	m.proposals[prop.ID] = prop
	log.Printf("[gov] proposal %s created by %s: %s → %s", prop.ID, proposerID, propType, newValue)
	return prop, nil
}

// Tick advances the governance state machine. Called each block.
// Moves pending proposals to active when voting should start.
// Finalizes active proposals when voting ends.
func (m *Module) Tick(currentBlock uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, prop := range m.proposals {
		switch prop.Status {
		case PropPending:
			if currentBlock >= uint64(prop.VotingStart) {
				prop.Status = PropActive
				log.Printf("[gov] proposal %s: voting open (block %d → %d)",
					prop.ID, prop.VotingStart, prop.VotingEnd)
			}
		case PropActive:
			if currentBlock >= uint64(prop.VotingEnd) {
				m.finalize(prop)
			}
		}
	}
}

// finalize determines whether a proposal passed or failed.
// Caller must hold m.mu Lock.
func (m *Module) finalize(prop *Proposal) {
	totalStake := m.totalStake()
	quorumStake := core.Amount(float64(totalStake) * m.config.QuorumPercent)

	totalVotes := prop.VotesYes + prop.VotesNo

	if totalVotes < quorumStake {
		prop.Status = PropRejected
		log.Printf("[gov] proposal %s: REJECTED (quorum not met: %d/%d votes, %d required)",
			prop.ID, totalVotes, totalStake, quorumStake)
		return
	}

	yesRatio := float64(prop.VotesYes) / float64(totalVotes)
	if yesRatio >= m.config.ThresholdPercent {
		prop.Status = PropPassed
		log.Printf("[gov] proposal %s: PASSED (%.1f%% yes, %d votes)",
			prop.ID, yesRatio*100, totalVotes)
	} else {
		prop.Status = PropRejected
		log.Printf("[gov] proposal %s: REJECTED (%.1f%% yes, threshold %.0f%%)",
			prop.ID, yesRatio*100, m.config.ThresholdPercent*100)
	}
}

// Execute applies a passed proposal. Only callable once per proposal.
func (m *Module) Execute(proposalID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	prop, ok := m.proposals[proposalID]
	if !ok {
		return fmt.Errorf("proposal %s not found", proposalID)
	}
	if prop.Status != PropPassed {
		return fmt.Errorf("proposal %s is not in passed state (%s)", proposalID, prop.Status)
	}

	prop.Status = PropExecuted
	prop.ExecutedAt = time.Now().Unix()
	log.Printf("[gov] proposal %s: EXECUTED — %s changed to %s", prop.ID, prop.Type, prop.NewValue)
	return nil
}

// ─── Voting ─────────────────────────────────────────────────────────────────

// Vote casts a vote from an agent on an active proposal.
// Voting power = agent stake + reputation bonus.
func (m *Module) Vote(agentID core.AgentID, proposalID string, choice bool, blockHeight uint64) (*Vote, error) {
	if agentID == "" {
		return nil, errors.New("agent ID required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prop, ok := m.proposals[proposalID]
	if !ok {
		return nil, fmt.Errorf("proposal %s not found", proposalID)
	}
	if prop.Status != PropActive {
		return nil, fmt.Errorf("proposal %s is not active (status: %s)", proposalID, prop.Status)
	}

	// Check for duplicate vote
	for _, v := range m.votes[proposalID] {
		if v.AgentID == agentID {
			return nil, fmt.Errorf("agent %s already voted on proposal %s", agentID, proposalID)
		}
	}

	// Calculate voting power
	votingPower := core.Amount(0)
	if m.registry != nil {
		agent, err := m.registry.GetAgent(agentID)
		if err == nil {
			votingPower = agent.Stake
			// Reputation bonus: up to +50% for Elite agents
			repBonus := core.Amount(float64(agent.Stake) * float64(agent.ReputationScore) / 200.0)
			if repBonus > agent.Stake/2 {
				repBonus = agent.Stake / 2
			}
			votingPower += repBonus
		}
	}

	vote := &Vote{
		AgentID:     agentID,
		ProposalID:  proposalID,
		Choice:      choice,
		VotingPower: votingPower,
		BlockHeight: blockHeight,
		Timestamp:   time.Now().Unix(),
	}

	m.votes[proposalID] = append(m.votes[proposalID], vote)

	if choice {
		prop.VotesYes += votingPower
	} else {
		prop.VotesNo += votingPower
	}

	log.Printf("[gov] agent %s voted %v on %s (power: %d)", agentID, choice, proposalID, votingPower)
	return vote, nil
}

// ─── Queries ────────────────────────────────────────────────────────────────

// GetProposal returns a proposal by ID.
func (m *Module) GetProposal(id string) (*Proposal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.proposals[id]
	if !ok {
		return nil, fmt.Errorf("proposal %s not found", id)
	}
	cp := *p
	return &cp, nil
}

// ListProposals returns all proposals, optionally filtered by status.
func (m *Module) ListProposals(status *ProposalStatus) []*Proposal {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Proposal, 0, len(m.proposals))
	for _, p := range m.proposals {
		if status != nil && p.Status != *status {
			continue
		}
		cp := *p
		result = append(result, &cp)
	}
	// Newest first
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result
}

// GetVotes returns all votes cast on a proposal.
func (m *Module) GetVotes(proposalID string) []*Vote {
	m.mu.RLock()
	defer m.mu.RUnlock()

	votes := m.votes[proposalID]
	result := make([]*Vote, len(votes))
	copy(result, votes)
	return result
}

// Stats returns governance statistics.
func (m *Module) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := map[ProposalStatus]int{}
	for _, p := range m.proposals {
		counts[p.Status]++
	}

	totalStake := m.totalStake()
	return map[string]interface{}{
		"total_proposals": len(m.proposals),
		"pending":         counts[PropPending],
		"active":          counts[PropActive],
		"passed":          counts[PropPassed],
		"rejected":        counts[PropRejected],
		"executed":        counts[PropExecuted],
		"total_stake":     totalStake,
		"quorum":          m.config.QuorumPercent,
		"threshold":       m.config.ThresholdPercent,
		"voting_blocks":   m.config.VotingBlocks,
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func (m *Module) totalStake() core.Amount {
	if m.registry == nil {
		return 0
	}
	var total core.Amount
	for _, agent := range m.registry.ListAgents(nil) {
		total += agent.Stake
	}
	return total
}

func genProposalID(proposer core.AgentID, title string) string {
	raw := fmt.Sprintf("%s:%s:%d", proposer, title, time.Now().UnixNano())
	h := sha256.Sum256([]byte(raw))
	return "prop_" + hex.EncodeToString(h[:])[:16]
}

// ─── Serialization ──────────────────────────────────────────────────────────

// MarshalJSON for Proposal omitted (uses standard json tags)

// UnmarshalJSON for Proposal (standard, json tags handle it)

// Export/Import for persistence
func (m *Module) Export() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state := struct {
		Proposals map[string]*Proposal `json:"proposals"`
		Votes     map[string][]*Vote   `json:"votes"`
		Config    Config               `json:"config"`
	}{
		Proposals: m.proposals,
		Votes:     m.votes,
		Config:    m.config,
	}
	return json.Marshal(state)
}

// Import restores governance state from serialized data.
func (m *Module) Import(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var state struct {
		Proposals map[string]*Proposal `json:"proposals"`
		Votes     map[string][]*Vote   `json:"votes"`
		Config    Config               `json:"config"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("unmarshal governance state: %w", err)
	}
	m.proposals = state.Proposals
	m.votes = state.Votes
	m.config = state.Config
	return nil
}
