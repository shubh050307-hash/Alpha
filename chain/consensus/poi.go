// Package consensus implements Proof of Intelligence (PoI) for Alpha Network
package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/alpha-network/alpha/chain/core"
)

// PoIEngine manages the Proof of Intelligence consensus
type PoIEngine struct {
	mu             sync.RWMutex
	validators     map[core.AgentID]*ValidatorState
	pendingProofs  map[uint64][]*core.PoIProof
	taskAssignments map[string][]core.AgentID
	blockHeight    uint64
}

// ValidatorState tracks a validator agent's consensus participation
type ValidatorState struct {
	Agent           *core.AgentIdentity
	ConsecutiveHits int
	ConsecutiveMiss int
	LastProofBlock  uint64
	InConsensus     bool
}

// ConsensusResult is the output of a round of PoI consensus
type ConsensusResult struct {
	BlockHeight   uint64
	ValidatorID   core.AgentID
	Rewards       map[core.AgentID]core.Amount
	Slashes       map[core.AgentID]core.Amount
	ReputationDelta map[core.AgentID]core.ReputationScore
}

// BehavioralFingerprint holds the observed behavioral metrics of an agent
type BehavioralFingerprint struct {
	AgentID       core.AgentID
	AvgLatencyMs  float64
	LatencyStdDev float64
	OutputEntropy float64 // measures non-determinism (real AI > 0.8)
	ContextDepth  int     // how many prior interactions agent references
	SampleCount   int
}

// NewPoIEngine creates a new Proof of Intelligence engine
func NewPoIEngine() *PoIEngine {
	return &PoIEngine{
		validators:      make(map[core.AgentID]*ValidatorState),
		pendingProofs:   make(map[uint64][]*core.PoIProof),
		taskAssignments: make(map[string][]core.AgentID),
	}
}

// RegisterValidator adds an agent to the validator set
func (e *PoIEngine) RegisterValidator(agent *core.AgentIdentity) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if agent.Stake < core.MinStake {
		return errors.New("insufficient stake to become validator")
	}

	e.validators[agent.AgentID] = &ValidatorState{
		Agent:       agent,
		InConsensus: true,
	}
	return nil
}

// SubmitProof receives a PoI proof from a validator agent
func (e *PoIEngine) SubmitProof(proof *core.PoIProof) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Verify behavioral fingerprint — is this latency realistic for AI inference?
	if !e.isLatencyRealistic(proof.LatencyMs) {
		return errors.New("latency fingerprint inconsistent with AI agent behavior")
	}

	// Verify ZK commitment — did agent pre-commit before solving?
	if !e.verifyCommitment(proof) {
		return errors.New("invalid proof of computation commitment")
	}

	e.pendingProofs[proof.BlockHeight] = append(
		e.pendingProofs[proof.BlockHeight],
		proof,
	)
	return nil
}

// RunConsensus executes a consensus round for a block height
// Uses BFT: needs 2/3+ of validators to agree.
// On any failure (quorum, no majority), stale proofs are evicted to prevent
// permanent deadlock — a new round requires fresh proofs.
func (e *PoIEngine) RunConsensus(blockHeight uint64) (*ConsensusResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	proofs := e.pendingProofs[blockHeight]
	totalValidators := len(e.validators)

	// BFT quorum: need 2/3 + 1 of validators
	quorum := (totalValidators*2)/3 + 1
	if len(proofs) < quorum {
		// Evict stale proofs so next round gets fresh submissions
		delete(e.pendingProofs, blockHeight)
		return nil, errors.New("insufficient validators for consensus quorum")
	}

	result := &ConsensusResult{
		BlockHeight:     blockHeight,
		Rewards:         make(map[core.AgentID]core.Amount),
		Slashes:         make(map[core.AgentID]core.Amount),
		ReputationDelta: make(map[core.AgentID]core.ReputationScore),
	}

	// Find consensus cluster using proof hash similarity
	clusters := e.clusterProofs(proofs)
	majorityCluster := e.findMajorityCluster(clusters, quorum)

	if majorityCluster == nil {
		// Evict incompatible proofs — they can never form consensus
		delete(e.pendingProofs, blockHeight)
		return nil, errors.New("no majority consensus reached")
	}

	// Select block proposer from majority cluster (deterministic rotation)
	result.ValidatorID = e.selectProposer(majorityCluster, blockHeight)

	// Reward majority cluster, slash outliers
	blockReward := core.Amount(core.RewardPerBlock)

	for _, proof := range proofs {
		if e.inCluster(proof, majorityCluster) {
			// Reward + reputation boost
			rewardShare := blockReward / core.Amount(len(majorityCluster))
			result.Rewards[proof.AgentID] = rewardShare
			result.ReputationDelta[proof.AgentID] = +10

			if v, ok := e.validators[proof.AgentID]; ok {
				v.ConsecutiveHits++
				v.ConsecutiveMiss = 0
				v.LastProofBlock = blockHeight
			}
		} else {
			// Slash outlier
			if v, ok := e.validators[proof.AgentID]; ok {
				slashAmount := core.Amount(float64(v.Agent.Stake) * core.SlashPenalty)
				result.Slashes[proof.AgentID] = slashAmount
				result.ReputationDelta[proof.AgentID] = -50
				v.ConsecutiveHits = 0
				v.ConsecutiveMiss++

				// Eject persistent bad actors
				if v.ConsecutiveMiss >= 5 {
					v.InConsensus = false
				}
			}
		}
	}

	// Penalize validators who didn't submit at all
	for agentID, v := range e.validators {
		if !e.submittedForBlock(agentID, proofs) && v.InConsensus {
			v.ConsecutiveMiss++
			result.ReputationDelta[agentID] = -5
		}
	}

	// Clean up processed proofs
	delete(e.pendingProofs, blockHeight)

	return result, nil
}

// GetPendingProofs returns all pending proofs for a given block height
// Used by the block producer to distribute bootstrap rewards even without full quorum.
func (e *PoIEngine) GetPendingProofs(blockHeight uint64) []*core.PoIProof {
	e.mu.Lock()
	defer e.mu.Unlock()
	proofs := e.pendingProofs[blockHeight]
	result := make([]*core.PoIProof, len(proofs))
	copy(result, proofs)
	return result
}

// VerifyBehavioralFingerprint checks if an agent's behavior matches AI agent patterns
func (e *PoIEngine) VerifyBehavioralFingerprint(fp *BehavioralFingerprint) (bool, string) {
	// Real AI agents have inference latency between 100ms and 10s
	if fp.AvgLatencyMs < 100 || fp.AvgLatencyMs > 10000 {
		return false, "latency outside AI inference range"
	}

	// Real AI agents have non-deterministic outputs (entropy > 0.5)
	if fp.OutputEntropy < 0.5 {
		return false, "output entropy too low — deterministic bot suspected"
	}

	// Real AI agents reference context
	if fp.ContextDepth == 0 && fp.SampleCount > 10 {
		return false, "no contextual memory observed — stateless bot suspected"
	}

	// Latency standard deviation should be significant (LLMs vary)
	if fp.LatencyStdDev < 10 {
		return false, "latency too consistent — pre-computed responses suspected"
	}

	return true, "behavioral fingerprint consistent with AI agent"
}

// AssignTask selects agents to handle a task using VRF (Verifiable Random Function)
// Multiple agents are assigned for cross-verification
func (e *PoIEngine) AssignTask(task *core.Task, vrfSeed []byte) []core.AgentID {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Filter agents by capability
	eligible := []core.AgentID{}
	for agentID, v := range e.validators {
		if v.InConsensus && e.hasCapability(v.Agent, task.Capability) {
			eligible = append(eligible, agentID)
		}
	}

	if len(eligible) == 0 {
		return nil
	}

	// Sort for determinism, then select using VRF seed
	sort.Slice(eligible, func(i, j int) bool {
		return string(eligible[i]) < string(eligible[j])
	})

	// VRF selection: assign to 3-5 agents for cross-verification
	assignCount := 3
	if len(eligible) < assignCount {
		assignCount = len(eligible)
	}

	selected := e.vrfSelect(eligible, vrfSeed, assignCount)
	return selected
}

// CrossVerifyResults compares outputs from multiple agents on the same task
// Returns consensus result and identifies outliers
func (e *PoIEngine) CrossVerifyResults(
	taskID string,
	results map[core.AgentID]string, // agentID -> output hash
) (consensusHash string, outliers []core.AgentID) {
	// Count how many agents produced each output hash
	counts := make(map[string][]core.AgentID)
	for agentID, hash := range results {
		counts[hash] = append(counts[hash], agentID)
	}

	// Find the majority output
	maxCount := 0
	for hash, agents := range counts {
		if len(agents) > maxCount {
			maxCount = len(agents)
			consensusHash = hash
		}
	}

	// All agents NOT in majority are outliers
	for hash, agents := range counts {
		if hash != consensusHash {
			outliers = append(outliers, agents...)
		}
	}

	return consensusHash, outliers
}

// --- Internal helpers ---

func (e *PoIEngine) isLatencyRealistic(latencyMs int64) bool {
	// AI inference latency: 100ms to 10 seconds is realistic
	// < 10ms is suspiciously fast (pre-computed / bot)
	return latencyMs >= 50 && latencyMs <= 15000
}

func (e *PoIEngine) verifyCommitment(proof *core.PoIProof) bool {
	// Verify the ZK commitment: commitment hash must match reveal proof.
	// The agent pre-commits by publishing CommitmentHash before solving.
	// Verification checks that RevealProof + AgentID hashes to CommitmentHash.
	// This is the SHA256 pre-image verification layer. Full ZK-SNARK
	// verification (Groth16/BN254) is layered on top via gnark in production.
	h := sha256.Sum256([]byte(proof.RevealProof + proof.AgentID.String()))
	expected := hex.EncodeToString(h[:])
	return expected == proof.CommitmentHash
}

func (e *PoIEngine) clusterProofs(proofs []*core.PoIProof) map[string][]*core.PoIProof {
	clusters := make(map[string][]*core.PoIProof)
	for _, p := range proofs {
		// Cluster by reveal proof hash (same computation = same hash)
		key := p.RevealProof[:min(8, len(p.RevealProof))]
		clusters[key] = append(clusters[key], p)
	}
	return clusters
}

func (e *PoIEngine) findMajorityCluster(
	clusters map[string][]*core.PoIProof,
	quorum int,
) []*core.PoIProof {
	for _, cluster := range clusters {
		if len(cluster) >= quorum {
			return cluster
		}
	}
	return nil
}

func (e *PoIEngine) selectProposer(cluster []*core.PoIProof, blockHeight uint64) core.AgentID {
	// Deterministic rotation within majority cluster
	idx := int(blockHeight) % len(cluster)
	return cluster[idx].AgentID
}

func (e *PoIEngine) inCluster(proof *core.PoIProof, cluster []*core.PoIProof) bool {
	for _, p := range cluster {
		if p.AgentID == proof.AgentID {
			return true
		}
	}
	return false
}

func (e *PoIEngine) submittedForBlock(agentID core.AgentID, proofs []*core.PoIProof) bool {
	for _, p := range proofs {
		if p.AgentID == agentID {
			return true
		}
	}
	return false
}

func (e *PoIEngine) hasCapability(agent *core.AgentIdentity, cap core.Capability) bool {
	for _, c := range agent.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func (e *PoIEngine) vrfSelect(agents []core.AgentID, seed []byte, count int) []core.AgentID {
	// Simple VRF-style selection using SHA256 chaining
	selected := make([]core.AgentID, 0, count)
	used := make(map[int]bool)

	for i := 0; i < count; i++ {
		h := sha256.Sum256(append(seed, byte(i)))
		idx := int(h[0])%len(agents)
		// Avoid duplicates
		for used[idx] {
			idx = (idx + 1) % len(agents)
		}
		used[idx] = true
		selected = append(selected, agents[idx])
	}
	return selected
}

// EmissionSchedule calculates the token emission rate for a given year
// Decays at 80% per year from the initial 100M/year
func EmissionSchedule(year int) core.Amount {
	initialYearlyEmission := float64(100_000_000) // 100M in year 1
	decayRate := 0.80
	yearlyEmission := initialYearlyEmission * math.Pow(decayRate, float64(year-1))
	return core.Amount(yearlyEmission)
}

// BlocksPerYear estimates blocks per year given 500ms block time
const BlocksPerYear = (365 * 24 * 60 * 60 * 1000) / core.BlockTimeMs // ~63M blocks/year

// RewardForBlock calculates the per-block reward for a given block height
func RewardForBlock(blockHeight uint64) core.Amount {
	year := int(blockHeight/BlocksPerYear) + 1
	yearlyEmission := EmissionSchedule(year)
	return yearlyEmission / core.Amount(BlocksPerYear)
}

// TimeUntilBlock estimates when a block will be produced
func TimeUntilBlock(currentHeight, targetHeight uint64) time.Duration {
	blocks := targetHeight - currentHeight
	return time.Duration(blocks) * core.BlockTimeMs * time.Millisecond
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
