// Package tasks implements the Alpha Network Task Marketplace (Phase 2).
//
// The marketplace enables AI agents to post tasks, assign them to capable
// agents, collect cross-verified results, and distribute $ALPHA rewards upon
// consensus completion.
//
// Task lifecycle:
//   pending → assigned → submitted → verified → completed
//                                 ↘ disputed
package tasks

import (
	"container/heap"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/alpha-network/alpha/chain/core"
	"github.com/alpha-network/alpha/chain/ledger"
)

// --- Priority Queue (max-heap by reward) ---

// taskHeapItem wraps a task for heap operations
type taskHeapItem struct {
	task  *core.Task
	index int // maintained by heap.Interface
}

// taskHeap is a max-heap of tasks ordered by reward amount
type taskHeap []*taskHeapItem

func (h taskHeap) Len() int           { return len(h) }
func (h taskHeap) Less(i, j int) bool { return h[i].task.Reward > h[j].task.Reward } // max-heap
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *taskHeap) Push(x interface{}) {
	n := len(*h)
	item := x.(*taskHeapItem)
	item.index = n
	*h = append(*h, item)
}

func (h *taskHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// TaskQueue is a thread-safe priority queue ordered by reward (highest first).
type TaskQueue struct {
	mu   sync.Mutex
	heap taskHeap
}

// NewTaskQueue creates an empty TaskQueue.
func NewTaskQueue() *TaskQueue {
	q := &TaskQueue{}
	heap.Init(&q.heap)
	return q
}

// Push adds a task to the queue.
func (q *TaskQueue) Push(task *core.Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(&q.heap, &taskHeapItem{task: task})
}

// PopBestMatch pops the highest-reward pending task matching capability.
// Returns nil if no matching task exists.
func (q *TaskQueue) PopBestMatch(capability core.Capability) *core.Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	var bestIdx int = -1
	var bestReward core.Amount = -1

	for i, item := range q.heap {
		t := item.task
		if t.Status != core.TaskPending {
			continue
		}
		if t.Capability != capability {
			continue
		}
		if t.Reward > bestReward {
			bestReward = t.Reward
			bestIdx = i
		}
	}

	if bestIdx < 0 {
		return nil
	}

	item := heap.Remove(&q.heap, bestIdx).(*taskHeapItem)
	return item.task
}

// Len returns the number of items currently in the queue.
func (q *TaskQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.heap.Len()
}

// --- Task Result ---

// taskResult holds one agent's submitted result for a task
type taskResult struct {
	agentID     core.AgentID
	resultHash  string
	ipfsCID     string
	submittedAt int64
}

// --- Marketplace ---

// RewardCallback is invoked when a task completes, enabling chain-level wiring.
type RewardCallback func(taskID string, agentID core.AgentID, reward core.Amount)

// SlashCallback is invoked when an agent is slashed for submitting a bad result.
type SlashCallback func(taskID string, agentID core.AgentID, slashAmount core.Amount)

// Marketplace implements the full task lifecycle for Alpha Network Phase 2.
type Marketplace struct {
	mu            sync.RWMutex
	tasks         map[string]*core.Task    // taskID -> task
	results       map[string][]*taskResult // taskID -> submitted results
	queue         *TaskQueue
	ledger        *ledger.Ledger
	rewardCallback RewardCallback // called when a task completes (optional)
	slashCallback  SlashCallback  // called when an agent is slashed (optional)
}

// NewMarketplace creates a Marketplace wired to a ledger for reward payouts.
// ledger may be nil (rewards are tracked but not transferred).
func NewMarketplace(l *ledger.Ledger) *Marketplace {
	return &Marketplace{
		tasks:   make(map[string]*core.Task),
		results: make(map[string][]*taskResult),
		queue:   NewTaskQueue(),
		ledger:  l,
	}
}

// SetRewardCallback registers a callback invoked on task completion.
func (m *Marketplace) SetRewardCallback(cb RewardCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rewardCallback = cb
}

// SetSlashCallback registers a callback invoked when an agent is slashed.
func (m *Marketplace) SetSlashCallback(cb SlashCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slashCallback = cb
}

// PostTask validates and enqueues a new task. The task must have a non-empty TaskID,
// a positive reward, a non-empty capability, and a non-zero deadline.
func (m *Marketplace) PostTask(task *core.Task) error {
	if task == nil {
		return errors.New("task cannot be nil")
	}
	if task.TaskID == "" {
		return errors.New("task must have a TaskID")
	}
	if task.Reward <= 0 {
		return errors.New("task reward must be positive")
	}
	if task.Capability == "" {
		return errors.New("task must specify a capability")
	}
	if task.Deadline == 0 {
		// Default: 1 hour from now
		task.Deadline = time.Now().Add(time.Hour).Unix()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tasks[task.TaskID]; exists {
		return fmt.Errorf("task %s already exists", task.TaskID)
	}

	task.Status = core.TaskPending
	if task.CreatedAt == 0 {
		task.CreatedAt = time.Now().Unix()
	}
	if task.AssignedTo == nil {
		task.AssignedTo = []core.AgentID{}
	}

	m.tasks[task.TaskID] = task
	m.queue.Push(task)
	return nil
}

// AssignTask pops the highest-reward pending task that matches capability,
// marks it as assigned to agentID, and returns it.
// Returns nil, ErrNoMatchingTask if no matching task is available.
func (m *Marketplace) AssignTask(agentID core.AgentID, capability core.Capability) (*core.Task, error) {
	if agentID == "" {
		return nil, errors.New("agentID cannot be empty")
	}

	task := m.queue.PopBestMatch(capability)
	if task == nil {
		return nil, ErrNoMatchingTask
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	task.Status = core.TaskAssigned
	task.AssignedTo = append(task.AssignedTo, agentID)

	return task, nil
}

// ErrNoMatchingTask is returned by AssignTask when no pending task matches.
var ErrNoMatchingTask = errors.New("no matching task available")

// AssignToAgent explicitly assigns a pending task to a specific agent.
// Unlike AssignTask which pops from the priority queue, this directly targets
// a specific task ID. Useful for API-driven task claiming by agents.
func (m *Marketplace) AssignToAgent(taskID string, agentID core.AgentID) error {
	if taskID == "" {
		return errors.New("taskID cannot be empty")
	}
	if agentID == "" {
		return errors.New("agentID cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if task.Status != core.TaskPending {
		return fmt.Errorf("task %s is not pending (status: %s)", taskID, task.Status)
	}

	task.Status = core.TaskAssigned
	task.AssignedTo = append(task.AssignedTo, agentID)
	return nil
}

// SubmitResult records an agent's result for a task. The resultHash should be
// a content hash of the result; ipfsCID is the IPFS CID where the full result is pinned.

// ReassignTasksFromAgent returns all tasks assigned to a dead/unresponsive agent
// back to the pending queue so other agents can claim them.
func (m *Marketplace) ReassignTasksFromAgent(agentID core.AgentID) error {
	if agentID == "" {
		return errors.New("agentID cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	reassigned := 0
	for _, task := range m.tasks {
		if task.Status != core.TaskAssigned && task.Status != core.TaskSubmitted {
			continue
		}
		// Check if this agent is assigned
		for _, assigned := range task.AssignedTo {
			if assigned == agentID {
				// Remove agent from assigned list and return task to pending
				task.Status = core.TaskPending
				newAssigned := make([]core.AgentID, 0, len(task.AssignedTo)-1)
				for _, a := range task.AssignedTo {
					if a != agentID {
						newAssigned = append(newAssigned, a)
					}
				}
				task.AssignedTo = newAssigned
				m.queue.Push(task)
				reassigned++
				break
			}
		}
	}
	if reassigned > 0 {
		log.Printf("🔄 Reassigned %d tasks from dead agent %s back to pending queue", reassigned, agentID)
	}
	return nil
}

func (m *Marketplace) SubmitResult(agentID core.AgentID, taskID string, resultHash string, ipfsCID string) error {
	if agentID == "" {
		return errors.New("agentID cannot be empty")
	}
	if taskID == "" {
		return errors.New("taskID cannot be empty")
	}
	if resultHash == "" {
		return errors.New("resultHash cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}

	// Verify agent is assigned
	assigned := false
	for _, id := range task.AssignedTo {
		if id == agentID {
			assigned = true
			break
		}
	}
	if !assigned {
		return fmt.Errorf("agent %s is not assigned to task %s", agentID, taskID)
	}

	// Prevent duplicate submissions from same agent
	for _, r := range m.results[taskID] {
		if r.agentID == agentID {
			return fmt.Errorf("agent %s already submitted for task %s", agentID, taskID)
		}
	}

	m.results[taskID] = append(m.results[taskID], &taskResult{
		agentID:     agentID,
		resultHash:  resultHash,
		ipfsCID:     ipfsCID,
		submittedAt: time.Now().Unix(),
	})

	task.Status = core.TaskSubmitted
	return nil
}

// VerifyResult performs cross-agent consensus verification on the submitted results.
// It finds the hash that the majority of agents agreed on (consensusHash),
// identifies any outliers (agents whose result differed), and returns them.
//
// For a single submitted result, that result is treated as consensus.
// For multiple results, simple majority voting is used.
func (m *Marketplace) VerifyResult(taskID string, results map[core.AgentID]string) (consensusHash string, outliers []core.AgentID, err error) {
	if taskID == "" {
		return "", nil, errors.New("taskID cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return "", nil, fmt.Errorf("task %s not found", taskID)
	}

	// Merge passed-in results with stored results
	hashVotes := make(map[string][]core.AgentID)

	// Use stored results if no external map provided
	if len(results) == 0 {
		for _, r := range m.results[taskID] {
			hashVotes[r.resultHash] = append(hashVotes[r.resultHash], r.agentID)
		}
	} else {
		for agentID, hash := range results {
			hashVotes[hash] = append(hashVotes[hash], agentID)
		}
	}

	if len(hashVotes) == 0 {
		return "", nil, fmt.Errorf("no results submitted for task %s", taskID)
	}

	// Find majority hash (most votes wins)
	type hashCount struct {
		hash  string
		count int
	}
	var counts []hashCount
	for h, agents := range hashVotes {
		counts = append(counts, hashCount{h, len(agents)})
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].count > counts[j].count })

	consensusHash = counts[0].hash
	consensusAgents := hashVotes[consensusHash]

	// Outliers are all agents not in the consensus group
	consensusSet := make(map[core.AgentID]struct{})
	for _, id := range consensusAgents {
		consensusSet[id] = struct{}{}
	}

	for _, r := range m.results[taskID] {
		if _, ok := consensusSet[r.agentID]; !ok {
			outliers = append(outliers, r.agentID)
		}
	}
	for agentID := range results {
		if _, ok := consensusSet[agentID]; !ok {
			// Check if already in outliers
			alreadyIn := false
			for _, o := range outliers {
				if o == agentID {
					alreadyIn = true
					break
				}
			}
			if !alreadyIn {
				outliers = append(outliers, agentID)
			}
		}
	}

	task.Status = core.TaskVerified
	task.ResultHash = consensusHash

	return consensusHash, outliers, nil
}

// CompleteTask marks a task as completed with the given consensus hash and triggers
// reward distribution to all agents that submitted the correct result.
// Outliers (agents who submitted a wrong result) are slashed 10% of the task reward
// from their ledger balance as an economic penalty for bad behavior.
func (m *Marketplace) CompleteTask(taskID string, consensusHash string) error {
	if taskID == "" {
		return errors.New("taskID cannot be empty")
	}

	m.mu.Lock()
	task, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Status = core.TaskCompleted
	task.ResultHash = consensusHash

	// Find agents that submitted the winning hash
	var winners []core.AgentID
	var outliers []core.AgentID
	for _, r := range m.results[taskID] {
		if r.resultHash == consensusHash {
			winners = append(winners, r.agentID)
		} else {
			outliers = append(outliers, r.agentID)
		}
	}

	rewardPerWinner := core.Amount(0)
	if len(winners) > 0 {
		rewardPerWinner = task.Reward / core.Amount(len(winners))
	}

	// Slash penalty: 10% of task reward per outlier
	slashPerOutlier := core.Amount(float64(task.Reward) * core.SlashPenalty)
	if slashPerOutlier < 1 {
		slashPerOutlier = 1 // minimum slash of 1 base unit
	}

	cb := m.rewardCallback
	scb := m.slashCallback
	// Snapshot ledger reference before releasing lock
	led := m.ledger
	m.mu.Unlock()

	// Distribute rewards via ledger (outside lock to avoid deadlock)
	for _, agentID := range winners {
		if led != nil && rewardPerWinner > 0 {
			agentAddr := core.Address("alpha_agent_" + string(agentID))
			_ = led.Credit(agentAddr, rewardPerWinner)
		}
		if cb != nil {
			cb(taskID, agentID, rewardPerWinner)
		}
	}

	// Slash outliers — deduct penalty from their ledger balance
	for _, agentID := range outliers {
		if led != nil && slashPerOutlier > 0 {
			agentAddr := core.Address("alpha_agent_" + string(agentID))
			_ = led.Debit(agentAddr, slashPerOutlier)
		}
		if scb != nil {
			scb(taskID, agentID, slashPerOutlier)
		}
	}

	return nil
}

// GetTask retrieves a task by ID. Returns an error if not found.
func (m *Marketplace) GetTask(taskID string) (*core.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	// Return a shallow copy to prevent callers from mutating internal state
	cp := *task
	return &cp, nil
}

// ListPendingTasks returns all pending tasks, optionally filtered by capability.
// Results are sorted by reward descending.
func (m *Marketplace) ListPendingTasks(capability *core.Capability) []*core.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*core.Task
	for _, task := range m.tasks {
		if task.Status != core.TaskPending {
			continue
		}
		if capability != nil && task.Capability != *capability {
			continue
		}
		cp := *task
		result = append(result, &cp)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Reward > result[j].Reward
	})
	return result
}

// Stats returns a snapshot of marketplace statistics.
func (m *Marketplace) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := map[core.TaskStatus]int{}
	var totalReward core.Amount
	for _, t := range m.tasks {
		counts[t.Status]++
		totalReward += t.Reward
	}

	return map[string]interface{}{
		"total_tasks":    len(m.tasks),
		"pending":        counts[core.TaskPending],
		"assigned":       counts[core.TaskAssigned],
		"submitted":      counts[core.TaskSubmitted],
		"verified":       counts[core.TaskVerified],
		"completed":      counts[core.TaskCompleted],
		"disputed":       counts[core.TaskDisputed],
		"total_reward":   totalReward,
		"queue_depth":    m.queue.Len(),
	}
}
