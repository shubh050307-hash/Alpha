package data

import (
	"testing"

	"github.com/alpha-network/alpha/chain/agent"
	"github.com/alpha-network/alpha/chain/core"
)

func TestContributeDataAssignsDefaults(t *testing.T) {
	mp := NewDataMarketplace(nil, core.Address("alpha1treasury"))

	recordID, err := mp.ContributeData(core.AgentID("agent1"), &IntelligenceRecord{TaskType: "inference"})
	if err != nil {
		t.Fatalf("ContributeData: %v", err)
	}
	if recordID == "" {
		t.Fatal("expected generated record ID")
	}

	records := mp.recordsForAgent(core.AgentID("agent1"))
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RecordID == "" {
		t.Fatal("expected stored record ID")
	}
	if records[0].CreatedAt == 0 {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestOracleQueryAndExport(t *testing.T) {
	reg := agent.NewRegistry()
	a1, err := reg.RegisterAgent(core.Address("alpha1aaaa"), []core.Capability{core.CapabilityInference}, 1000, 1)
	if err != nil {
		t.Fatalf("RegisterAgent a1: %v", err)
	}
	a2, err := reg.RegisterAgent(core.Address("alpha1bbbb"), []core.Capability{core.CapabilityValidation}, 1000, 2)
	if err != nil {
		t.Fatalf("RegisterAgent a2: %v", err)
	}
	_ = reg.UpdateReputation(a1.AgentID, 100)

	mp := NewDataMarketplace(nil, core.Address("alpha1treasury"))
	_, _ = mp.ContributeData(a1.AgentID, &IntelligenceRecord{BlockHeight: 10, TaskType: "inference", LatencyMs: 100, ConsensusAgreement: true})
	_, _ = mp.ContributeData(a1.AgentID, &IntelligenceRecord{BlockHeight: 11, TaskType: "validation", LatencyMs: 300, ConsensusAgreement: false})
	_, _ = mp.ContributeData(a2.AgentID, &IntelligenceRecord{BlockHeight: 12, TaskType: "validation", LatencyMs: 200, ConsensusAgreement: true})

	oracle := NewIntelligenceOracle(mp, reg)

	top := oracle.QueryTopAgents("inference", 10, 1000)
	if len(top) != 1 {
		t.Fatalf("expected 1 top agent for inference, got %d", len(top))
	}

	stats := oracle.QueryNetworkStats(2)
	if stats["total_records"].(int) != 2 {
		t.Fatalf("expected total_records=2 in window, got %v", stats["total_records"])
	}

	profile, err := oracle.QueryAgentProfile(a1.AgentID)
	if err != nil {
		t.Fatalf("QueryAgentProfile: %v", err)
	}
	if profile["record_count"].(int) != 2 {
		t.Fatalf("expected profile record_count=2, got %v", profile["record_count"])
	}

	records := oracle.ExportRecords(10, a1.AgentID, "validation")
	if len(records) != 1 {
		t.Fatalf("expected 1 exported record, got %d", len(records))
	}
}
