package data

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alpha-network/alpha/chain/agent"
	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/ledger"
)

// IntelligenceRecord captures a single behavioral snapshot produced by an agent.
type IntelligenceRecord struct {
	RecordID           string               `json:"record_id"`
	AgentID            core.AgentID         `json:"agent_id"`
	BlockHeight        uint64               `json:"block_height"`
	TaskType           string               `json:"task_type"`
	LatencyMs          int64                `json:"latency_ms"`
	OutputEntropy      float64              `json:"output_entropy"`
	ConsensusAgreement bool                 `json:"consensus_agreement"`
	ReputationDelta    core.ReputationScore `json:"reputation_delta"`
	CreatedAt          int64                `json:"created_at"`
}

// DataMarketplace stores intelligence records that can be queried by the oracle.
type DataMarketplace struct {
	mu       sync.RWMutex
	ledger   *ledger.Ledger
	treasury core.Address

	recordsByAgent map[core.AgentID][]*IntelligenceRecord
	allRecords     []*IntelligenceRecord
	recordCounter  uint64
}

// NewDataMarketplace creates an in-memory data marketplace.
func NewDataMarketplace(l *ledger.Ledger, treasury core.Address) *DataMarketplace {
	return &DataMarketplace{
		ledger:         l,
		treasury:       treasury,
		recordsByAgent: make(map[core.AgentID][]*IntelligenceRecord),
		allRecords:     make([]*IntelligenceRecord, 0),
	}
}

// ContributeData stores a new intelligence record and returns its record ID.
func (m *DataMarketplace) ContributeData(agentID core.AgentID, rec *IntelligenceRecord) (string, error) {
	if rec == nil {
		return "", errors.New("intelligence record cannot be nil")
	}
	if agentID == "" {
		if rec.AgentID == "" {
			return "", errors.New("agent_id required")
		}
		agentID = rec.AgentID
	}

	cp := *rec
	cp.AgentID = agentID
	if cp.RecordID == "" {
		n := atomic.AddUint64(&m.recordCounter, 1)
		cp.RecordID = fmt.Sprintf("ir_%s_%d", sanitizeID(agentID), n)
	}
	if cp.CreatedAt == 0 {
		cp.CreatedAt = time.Now().Unix()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.allRecords = append(m.allRecords, &cp)
	m.recordsByAgent[agentID] = append(m.recordsByAgent[agentID], &cp)
	return cp.RecordID, nil
}

func (m *DataMarketplace) recordsSnapshot() []*IntelligenceRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*IntelligenceRecord, 0, len(m.allRecords))
	for _, rec := range m.allRecords {
		cp := *rec
		out = append(out, &cp)
	}
	return out
}

func (m *DataMarketplace) recordsForAgent(agentID core.AgentID) []*IntelligenceRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	records := m.recordsByAgent[agentID]
	out := make([]*IntelligenceRecord, 0, len(records))
	for _, rec := range records {
		cp := *rec
		out = append(out, &cp)
	}
	return out
}

// IntelligenceOracle answers read queries over marketplace and registry data.
type IntelligenceOracle struct {
	marketplace *DataMarketplace
	registry    *agent.Registry
}

// NewIntelligenceOracle creates an oracle bound to the data marketplace and registry.
func NewIntelligenceOracle(mp *DataMarketplace, reg *agent.Registry) *IntelligenceOracle {
	return &IntelligenceOracle{marketplace: mp, registry: reg}
}

// QueryNetworkStats returns aggregate intelligence metrics.
func (o *IntelligenceOracle) QueryNetworkStats(window uint64) map[string]interface{} {
	records := o.filterByWindow(o.marketplace.recordsSnapshot(), window)
	activeAgents := 0
	if o.registry != nil {
		activeAgents = len(o.registry.ListAgents(nil))
	}

	if len(records) == 0 {
		return map[string]interface{}{
			"time_window_blocks":           window,
			"avg_latency_ms":               0,
			"throughput_records_per_block": 0.0,
			"consensus_rate":               0.0,
			"total_records":                0,
			"active_agents":                activeAgents,
		}
	}

	var totalLatency int64
	consensusCount := 0
	for _, rec := range records {
		totalLatency += rec.LatencyMs
		if rec.ConsensusAgreement {
			consensusCount++
		}
	}

	denom := float64(window)
	if denom <= 0 {
		denom = 1
	}

	return map[string]interface{}{
		"time_window_blocks":           window,
		"avg_latency_ms":               float64(totalLatency) / float64(len(records)),
		"throughput_records_per_block": float64(len(records)) / denom,
		"consensus_rate":               float64(consensusCount) / float64(len(records)),
		"total_records":                len(records),
		"active_agents":                activeAgents,
	}
}

// QueryTopAgents returns top agents by reputation, optionally filtered by capability.
func (o *IntelligenceOracle) QueryTopAgents(capability string, limit int, _ uint64) []map[string]interface{} {
	if o.registry == nil {
		return []map[string]interface{}{}
	}

	var capFilter *core.Capability
	if capability != "" {
		c := core.Capability(capability)
		capFilter = &c
	}

	agents := o.registry.TopAgents(limit, capFilter)
	out := make([]map[string]interface{}, 0, len(agents))
	for _, a := range agents {
		trust, err := o.registry.TrustScore(a.AgentID)
		if err != nil {
			trust = 0
		}
		out = append(out, map[string]interface{}{
			"agent_id":         a.AgentID,
			"address":          a.Address,
			"reputation_score": a.ReputationScore,
			"stake":            a.Stake,
			"task_count":       a.TaskCount,
			"capabilities":     a.Capabilities,
			"status":           a.Status,
			"trust_score":      trust,
		})
	}
	return out
}

// QueryAgentProfile returns intelligence profile data for a specific agent.
func (o *IntelligenceOracle) QueryAgentProfile(agentID core.AgentID) (map[string]interface{}, error) {
	if o.registry == nil {
		return nil, errors.New("agent registry not available")
	}

	a, err := o.registry.GetAgent(agentID)
	if err != nil {
		return nil, err
	}

	trust, err := o.registry.TrustScore(a.AgentID)
	if err != nil {
		trust = 0
	}
	records := o.marketplace.recordsForAgent(a.AgentID)

	return map[string]interface{}{
		"agent":          a,
		"record_count":   len(records),
		"trust_score":    trust,
		"latest_records": records,
	}, nil
}

// ExportRecords returns intelligence records, filtered and capped.
func (o *IntelligenceOracle) ExportRecords(limit int, agentFilter core.AgentID, taskTypeFilter string) []*IntelligenceRecord {
	records := o.marketplace.recordsSnapshot()
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}

	out := make([]*IntelligenceRecord, 0)
	for _, rec := range records {
		if agentFilter != "" && rec.AgentID != agentFilter {
			continue
		}
		if taskTypeFilter != "" && !strings.EqualFold(rec.TaskType, taskTypeFilter) {
			continue
		}
		out = append(out, rec)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (o *IntelligenceOracle) filterByWindow(records []*IntelligenceRecord, window uint64) []*IntelligenceRecord {
	if window == 0 || len(records) == 0 {
		return records
	}

	maxHeight := uint64(0)
	for _, rec := range records {
		if rec.BlockHeight > maxHeight {
			maxHeight = rec.BlockHeight
		}
	}

	if maxHeight < window {
		return records
	}
	minHeight := maxHeight - window + 1

	out := make([]*IntelligenceRecord, 0, len(records))
	for _, rec := range records {
		if rec.BlockHeight >= minHeight {
			out = append(out, rec)
		}
	}
	return out
}

func sanitizeID(agentID core.AgentID) string {
	s := strings.ReplaceAll(string(agentID), " ", "_")
	s = strings.ReplaceAll(s, "/", "_")
	if s == "" {
		return "unknown"
	}
	return s
}
