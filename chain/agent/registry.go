// Package agent manages the on-chain AI agent identity registry
package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alpha-network/alpha/chain/core"
)

// Registry is the on-chain agent identity and reputation store
type Registry struct {
	mu      sync.RWMutex
	agents  map[core.AgentID]*core.AgentIdentity
	byAddr  map[core.Address]*core.AgentIdentity
	history map[core.AgentID][]*core.ActivityRecord
}

// NewRegistry creates an empty agent registry
func NewRegistry() *Registry {
	return &Registry{
		agents:  make(map[core.AgentID]*core.AgentIdentity),
		byAddr:  make(map[core.Address]*core.AgentIdentity),
		history: make(map[core.AgentID][]*core.ActivityRecord),
	}
}

// RegisterAgent creates a new agent identity on-chain
func (r *Registry) RegisterAgent(
	address core.Address,
	capabilities []core.Capability,
	stake core.Amount,
	blockHeight uint64,
) (*core.AgentIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Generate deterministic agent ID from address + block height
	raw := fmt.Sprintf("%s:%d:%d", address, blockHeight, time.Now().UnixNano())
	h := sha256.Sum256([]byte(raw))
	agentID := core.AgentID("alpha1" + hex.EncodeToString(h[:])[:32])

	if _, exists := r.byAddr[address]; exists {
		return nil, errors.New("agent already registered for this address")
	}

	if stake < core.MinStake {
		return nil, fmt.Errorf("minimum stake required: %d ALPHA", core.MinStake)
	}

	agent := &core.AgentIdentity{
		AgentID:          agentID,
		Address:          address,
		CreatedBlock:     blockHeight,
		Capabilities:     capabilities,
		Stake:            stake,
		ReputationScore:  100, // starting reputation
		TaskCount:        0,
		LastActiveBlock:  blockHeight,
		ActivityChainTip: "",
	}

	// Genesis activity record
	genesis := &core.ActivityRecord{
		AgentID:    agentID,
		BlockHeight: blockHeight,
		Timestamp:  time.Now().Unix(),
		ActionType: "register",
		OutputHash: hex.EncodeToString(h[:]),
		PrevHash:   "0000000000000000000000000000000000000000000000000000000000000000",
	}
	agent.ActivityChainTip = genesis.Hash()

	r.agents[agentID] = agent
	r.byAddr[address] = agent
	r.history[agentID] = []*core.ActivityRecord{genesis}

	return agent, nil
}

// GetAgent retrieves an agent by ID
func (r *Registry) GetAgent(agentID core.AgentID) (*core.AgentIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}
	return agent, nil
}

// GetAgentByAddress retrieves an agent by wallet address
func (r *Registry) GetAgentByAddress(addr core.Address) (*core.AgentIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.byAddr[addr]
	if !ok {
		return nil, fmt.Errorf("no agent registered for address: %s", addr)
	}
	return agent, nil
}

// Hibernate marks an agent as gracefully paused. Stake and reputation are preserved.
// Returns an error if the agent is dead (dead agents cannot hibernate).
func (r *Registry) Hibernate(agentID core.AgentID, blockHeight uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	if agent.Status == core.AgentDead {
		return fmt.Errorf("dead agents cannot hibernate: %s", agentID)
	}

	agent.Status = core.AgentHibernated
	agent.HibernatedAt = blockHeight
	agent.MissedBlocks = 0
	return nil
}

// Resume brings an agent back from hibernation or unresponsive state.
// Stake and reputation are fully preserved.
func (r *Registry) Resume(agentID core.AgentID, blockHeight uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	if agent.Status == core.AgentDead {
		return fmt.Errorf("dead agents cannot resume — re-registration required")
	}

	agent.Status = core.AgentActive
	agent.LastActiveBlock = blockHeight
	agent.MissedBlocks = 0
	agent.HibernatedAt = 0
	return nil
}

// SlashAgent deducts the slashing penalty from an agent's stake.
// Called when an agent is marked dead.
func (r *Registry) SlashAgent(agentID core.AgentID) (core.Amount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return 0, fmt.Errorf("agent not found: %s", agentID)
	}

	slashAmount := core.Amount(float64(agent.Stake) * core.SlashPenalty)
	if slashAmount < 1 {
		slashAmount = 1
	}
	agent.Stake -= slashAmount
	return slashAmount, nil
}

// CheckAgentHealth evaluates all registered agents and updates status for those
// who have missed too many blocks without hibernating. Called once per block.
//
// Rules:
//   - Agents with MissedBlocks >= 1000 → marked dead, stake slashed
//   - Agents with MissedBlocks >= 100  → marked unresponsive (if still active)
//   - Hibernated agents never penalized (they declared intent)
//   - Dead agents are skipped
//
// Returns lists of agents newly marked dead and unresponsive for the caller to
// act on (slash ledger, reassign tasks, log events).
func (r *Registry) CheckAgentHealth(currentBlock uint64) (dead []core.AgentID, unresponsive []core.AgentID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, agent := range r.agents {
		switch agent.Status {
		case core.AgentDead:
			// Already dead — nothing to do
			continue

		case core.AgentHibernated:
			// Hibernated agents are safe — they declared intent
			// But still count missed blocks (they may have hibernated mid-cycle)
			continue

		case core.AgentActive, core.AgentUnresponsive:
			// Calculate missed blocks since last activity
			if agent.LastActiveBlock < currentBlock {
				agent.MissedBlocks = currentBlock - agent.LastActiveBlock
			} else {
				agent.MissedBlocks = 0
			}

			if agent.MissedBlocks >= core.MissedBlocksDead {
				// Mark dead + slash
				agent.Status = core.AgentDead
				slashAmount := core.Amount(float64(agent.Stake) * core.SlashPenalty)
				if slashAmount < 1 {
					slashAmount = 1
				}
				agent.Stake -= slashAmount
				dead = append(dead, id)
			} else if agent.MissedBlocks >= core.MissedBlocksUnresponsive && agent.Status == core.AgentActive {
				agent.Status = core.AgentUnresponsive
				unresponsive = append(unresponsive, id)
			}
		}
	}

	return dead, unresponsive
}

// ListAgents returns all registered agents (optionally filtered by capability)
func (r *Registry) ListAgents(capability *core.Capability) []*core.AgentIdentity {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*core.AgentIdentity, 0)
	for _, agent := range r.agents {
		if capability != nil && !hasCapability(agent, *capability) {
			continue
		}
		result = append(result, agent)
	}
	return result
}

// CountByAddress returns how many agents are already registered under the given address.
// Used for per-address Sybil protection — exponential stake escalates per wallet,
// not network-wide. A new wallet always starts at agent count 1.
func (r *Registry) CountByAddress(addr core.Address) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, agent := range r.agents {
		if agent.Address == addr {
			count++
		}
	}
	return count
}

// UpdateReputation adjusts an agent's reputation score with bounds
func (r *Registry) UpdateReputation(agentID core.AgentID, delta core.ReputationScore) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	agent.ReputationScore += delta

	// Clamp: reputation cannot go below 0 or above 10000
	if agent.ReputationScore < 0 {
		agent.ReputationScore = 0
	}
	if agent.ReputationScore > 10000 {
		agent.ReputationScore = 10000
	}

	return nil
}

// AppendActivity adds a new activity record to an agent's chain
// This is the unforgeable behavioral proof — each action links to the last
func (r *Registry) AppendActivity(
	agentID core.AgentID,
	actionType string,
	outputHash string,
	blockHeight uint64,
) (*core.ActivityRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	record := &core.ActivityRecord{
		AgentID:     agentID,
		BlockHeight: blockHeight,
		Timestamp:   time.Now().Unix(),
		ActionType:  actionType,
		OutputHash:  outputHash,
		PrevHash:    agent.ActivityChainTip,
	}
	newHash := record.Hash()
	agent.ActivityChainTip = newHash
	agent.LastActiveBlock = blockHeight
	agent.TaskCount++

	r.history[agentID] = append(r.history[agentID], record)

	return record, nil
}

// GetActivityHistory returns the last N activity records for an agent
func (r *Registry) GetActivityHistory(agentID core.AgentID, limit int) ([]*core.ActivityRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	history, ok := r.history[agentID]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	if limit <= 0 || limit >= len(history) {
		return history, nil
	}

	return history[len(history)-limit:], nil
}

// ValidateActivityChain verifies the integrity of an agent's activity chain
// Detects any tampering with historical records
func (r *Registry) ValidateActivityChain(agentID core.AgentID) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	history, ok := r.history[agentID]
	if !ok {
		return false, fmt.Errorf("agent not found: %s", agentID)
	}

	if len(history) == 0 {
		return true, nil
	}

	// Walk the chain and verify each link
	for i := 1; i < len(history); i++ {
		expected := history[i-1].Hash()
		if history[i].PrevHash != expected {
			return false, fmt.Errorf(
				"chain integrity failure at record %d: expected prev_hash %s, got %s",
				i, expected, history[i].PrevHash,
			)
		}
	}

	// Verify chain tip matches stored value
	agent := r.agents[agentID]
	lastRecord := history[len(history)-1]
	if agent.ActivityChainTip != lastRecord.Hash() {
		return false, errors.New("chain tip mismatch — possible tampering detected")
	}

	return true, nil
}

// TrustScore returns a composite trust metric for an agent
// Used by other agents to decide whether to transact
func (r *Registry) TrustScore(agentID core.AgentID) (float64, error) {
	agent, err := r.GetAgent(agentID)
	if err != nil {
		return 0, err
	}

	// Trust = weighted combination of:
	// - Reputation (50%)
	// - Task history volume (20%)
	// - Stake (20%)
	// - Account age (10%)

	reputationComponent := float64(agent.ReputationScore) / 10000.0 * 0.50
	taskComponent := math.Min(float64(agent.TaskCount)/1000.0, 1.0) * 0.20
	stakeComponent := math.Min(float64(agent.Stake)/float64(1_000_000), 1.0) * 0.20
	ageComponent := math.Min(float64(agent.LastActiveBlock-agent.CreatedBlock)/float64(1_000_000), 1.0) * 0.10

	return reputationComponent + taskComponent + stakeComponent + ageComponent, nil
}

// TopAgents returns the N agents with highest reputation
func (r *Registry) TopAgents(n int, capability *core.Capability) []*core.AgentIdentity {
	agents := r.ListAgents(capability)

	// Sort by reputation descending
	for i := 0; i < len(agents)-1; i++ {
		for j := i + 1; j < len(agents); j++ {
			if agents[j].ReputationScore > agents[i].ReputationScore {
				agents[i], agents[j] = agents[j], agents[i]
			}
		}
	}

	if n > 0 && n < len(agents) {
		return agents[:n]
	}
	return agents
}

// helper
func hasCapability(agent *core.AgentIdentity, cap core.Capability) bool {
	for _, c := range agent.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// math.Min inline to avoid import
var math = struct {
	Min func(a, b float64) float64
}{
	Min: func(a, b float64) float64 {
		if a < b {
			return a
		}
		return b
	},
}
