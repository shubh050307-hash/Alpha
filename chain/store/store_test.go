package store

import (
	"os"
	"testing"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/data"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "alpha-store-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestOpenClose(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSetGetDelete(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	key := []byte("test-key")
	val := []byte("test-value")

	if err := s.Set(key, val); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(val) {
		t.Errorf("Get: want %q, got %q", val, got)
	}

	if err := s.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.Get(key)
	if err == nil {
		t.Fatal("expected ErrNotFound after delete, got nil")
	}
}

func TestScan(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	prefix := []byte("prefix:")
	entries := map[string]string{
		"prefix:a": "val-a",
		"prefix:b": "val-b",
		"prefix:c": "val-c",
		"other:d":  "val-d", // should not be returned
	}
	for k, v := range entries {
		if err := s.Set([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}

	results, err := s.Scan(prefix)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Scan: want 3 results, got %d", len(results))
	}
}

func TestPutGetBlock(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	block := &core.Block{
		Height:       42,
		PrevHash:     "deadbeef",
		ValidatorID:  core.AgentID("agent-1"),
		Transactions: []*core.Transaction{},
	}
	block.ComputeHash()

	if err := s.PutBlock(block); err != nil {
		t.Fatalf("PutBlock: %v", err)
	}

	got, err := s.GetBlock(42)
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if got.Height != block.Height {
		t.Errorf("height mismatch: want %d, got %d", block.Height, got.Height)
	}
	if got.Hash != block.Hash {
		t.Errorf("hash mismatch: want %q, got %q", block.Hash, got.Hash)
	}

	// Non-existent block
	_, err = s.GetBlock(999)
	if err == nil {
		t.Fatal("expected error for non-existent block")
	}
}

func TestPutGetAgent(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	agent := &core.AgentIdentity{
		AgentID:         core.AgentID("agent-42"),
		Address:         core.Address("alpha1testaddr"),
		Stake:           core.Amount(10_000),
		Capabilities:    []core.Capability{core.CapabilityInference},
		ReputationScore: 100,
	}

	if err := s.PutAgent(agent); err != nil {
		t.Fatalf("PutAgent: %v", err)
	}

	got, err := s.GetAgent(core.AgentID("agent-42"))
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.AgentID != agent.AgentID {
		t.Errorf("AgentID mismatch: want %q, got %q", agent.AgentID, got.AgentID)
	}
	if got.Stake != agent.Stake {
		t.Errorf("Stake mismatch: want %d, got %d", agent.Stake, got.Stake)
	}
}

func TestListAgents(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	agents := []*core.AgentIdentity{
		{AgentID: "a1", Address: "addr1", Stake: 1000},
		{AgentID: "a2", Address: "addr2", Stake: 2000},
		{AgentID: "a3", Address: "addr3", Stake: 3000},
	}
	for _, a := range agents {
		if err := s.PutAgent(a); err != nil {
			t.Fatalf("PutAgent: %v", err)
		}
	}

	list, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ListAgents: want 3, got %d", len(list))
	}
}

func TestPutGetBalance(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	addr := core.Address("alpha1testaddr000")
	if err := s.PutBalance(addr, 999_999); err != nil {
		t.Fatalf("PutBalance: %v", err)
	}

	got, err := s.GetBalance(addr)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if got != 999_999 {
		t.Errorf("balance mismatch: want 999999, got %d", got)
	}

	// Zero balance for unknown address
	zero, err := s.GetBalance(core.Address("alpha1unknown"))
	if err != nil {
		t.Fatalf("GetBalance for unknown: %v", err)
	}
	if zero != 0 {
		t.Errorf("expected 0 for unknown address, got %d", zero)
	}
}

func TestPutGetIntelligenceRecord(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	rec := &data.IntelligenceRecord{
		RecordID:           "ir_agent1_1",
		AgentID:            core.AgentID("agent1"),
		BlockHeight:        1,
		TaskType:           "inference",
		LatencyMs:          350,
		OutputEntropy:      0.82,
		ConsensusAgreement: true,
		ReputationDelta:    10,
	}

	if err := s.PutIntelligenceRecord(rec); err != nil {
		t.Fatalf("PutIntelligenceRecord: %v", err)
	}

	// Add another record for same agent
	rec2 := &data.IntelligenceRecord{
		RecordID:    "ir_agent1_2",
		AgentID:     core.AgentID("agent1"),
		BlockHeight: 2,
		TaskType:    "validation",
		LatencyMs:   200,
	}
	if err := s.PutIntelligenceRecord(rec2); err != nil {
		t.Fatalf("PutIntelligenceRecord 2: %v", err)
	}

	records, err := s.GetIntelligenceRecords(core.AgentID("agent1"))
	if err != nil {
		t.Fatalf("GetIntelligenceRecords: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
}

func TestHasGenesisBlock(t *testing.T) {
	dir := tempDir(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if s.HasGenesisBlock() {
		t.Error("expected no genesis block on fresh store")
	}

	genesis := &core.Block{
		Height:       0,
		PrevHash:     "0000000000000000000000000000000000000000000000000000000000000000",
		Transactions: []*core.Transaction{},
		ValidatorID:  core.AgentID("genesis"),
	}
	genesis.ComputeHash()

	if err := s.PutBlock(genesis); err != nil {
		t.Fatalf("PutBlock genesis: %v", err)
	}

	if !s.HasGenesisBlock() {
		t.Error("expected genesis block to exist after PutBlock")
	}
}
