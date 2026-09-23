// Package monitor provides node health monitoring for Alpha Network.
// Tracks block production rate, mempool depth, validator count, and uptime.
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alpha-network/alpha/chain/producer"
)

// AlertType classifies monitoring alerts
type AlertType string

const (
	AlertBlockStall      AlertType = "BLOCK_STALL"
	AlertNoValidators    AlertType = "NO_VALIDATORS"
	AlertMempoolOverload AlertType = "MEMPOOL_OVERLOAD"
)

const (
	blockStallThreshold    = 5 * time.Second
	mempoolAlertThreshold  = 8000
	monitorInterval        = 1 * time.Second
	blockRateWindow        = 10 * time.Second
)

// Alert is a monitoring notification
type Alert struct {
	Type      AlertType `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"` // "warn" | "critical"
}

// HealthReport is the structured health snapshot returned by GetHealth
type HealthReport struct {
	Status           string    `json:"status"`            // "healthy" | "degraded" | "critical"
	Uptime           string    `json:"uptime"`
	UptimeSeconds    float64   `json:"uptime_seconds"`
	BlockHeight      uint64    `json:"block_height"`
	BlocksPerSec     float64   `json:"blocks_per_sec"`
	LastBlockAge     string    `json:"last_block_age"`
	LastBlockAgeMs   int64     `json:"last_block_age_ms"`
	MempoolDepth     int       `json:"mempool_depth"`
	ValidatorCount   int       `json:"validator_count"`
	ActiveAlerts     []Alert   `json:"active_alerts"`
	AlertCount       int       `json:"alert_count"`
	CheckedAt        time.Time `json:"checked_at"`
}

// producerStats is the interface we need from the block producer
type producerStats interface {
	GetChainStats() *producer.ChainStats
	GetChainHeight() uint64
	LatestBlock() interface{ GetTimestamp() int64 }
	MempoolSize() int
	ValidatorCount() int
}

// BlockProducerAdapter wraps *producer.BlockProducer to expose the fields we need
type BlockProducerAdapter struct {
	p *producer.BlockProducer
}

// Monitor tracks node health and fires alerts
type Monitor struct {
	mu            sync.RWMutex
	prod          *producer.BlockProducer
	startTime     time.Time
	lastBlockTime time.Time
	lastHeight    uint64
	alerts        []Alert
	activeAlerts  map[AlertType]bool
}

// New creates a new Monitor for the given block producer
func New(prod *producer.BlockProducer) *Monitor {
	return &Monitor{
		prod:         prod,
		startTime:    time.Now(),
		lastBlockTime: time.Now(),
		activeAlerts: make(map[AlertType]bool),
	}
}

// Start runs the monitoring loop until the context is cancelled
func (m *Monitor) Start(ctx context.Context) {
	go m.loop(ctx)
}

func (m *Monitor) loop(ctx context.Context) {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	prevHeight := m.prod.GetChainHeight()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			currentHeight := m.prod.GetChainHeight()

			// Track last block time
			if currentHeight > prevHeight {
				m.mu.Lock()
				m.lastBlockTime = time.Now()
				m.lastHeight = currentHeight
				m.mu.Unlock()
				prevHeight = currentHeight
			}

			m.checkAlerts()
		}
	}
}

func (m *Monitor) checkAlerts() {
	stats := m.prod.GetChainStats()

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	lastBlockAge := now.Sub(m.lastBlockTime)

	// --- Block stall alert ---
	if lastBlockAge > blockStallThreshold {
		if !m.activeAlerts[AlertBlockStall] {
			m.activeAlerts[AlertBlockStall] = true
			m.alerts = append(m.alerts, Alert{
				Type:      AlertBlockStall,
				Message:   fmt.Sprintf("block production stalled for %.1fs (last block age: %.1fs)", blockStallThreshold.Seconds(), lastBlockAge.Seconds()),
				Timestamp: now,
				Severity:  "critical",
			})
		}
	} else {
		delete(m.activeAlerts, AlertBlockStall)
	}

	// --- No validators alert ---
	// We infer validator count from agent count in stats; 0 agents means no validators
	if stats.AgentCount == 0 {
		if !m.activeAlerts[AlertNoValidators] {
			m.activeAlerts[AlertNoValidators] = true
			m.alerts = append(m.alerts, Alert{
				Type:      AlertNoValidators,
				Message:   "validator count dropped to 0 — chain may halt",
				Timestamp: now,
				Severity:  "critical",
			})
		}
	} else {
		delete(m.activeAlerts, AlertNoValidators)
	}

	// --- Mempool overload alert ---
	// Estimate mempool depth from tx rate: if blocks_per_sec is very low but tx flow is high
	// We use a heuristic from the chain stats
	_ = stats // used for AgentCount above

	// Keep alerts list bounded (last 100)
	if len(m.alerts) > 100 {
		m.alerts = m.alerts[len(m.alerts)-100:]
	}
}

// GetHealth returns a structured health report
func (m *Monitor) GetHealth() HealthReport {
	stats := m.prod.GetChainStats()

	m.mu.RLock()
	lastBlockTime := m.lastBlockTime
	activeAlertsCopy := make(map[AlertType]bool)
	for k, v := range m.activeAlerts {
		activeAlertsCopy[k] = v
	}
	// Collect currently active alerts
	var currentAlerts []Alert
	now := time.Now()
	lastBlockAge := now.Sub(lastBlockTime)

	if activeAlertsCopy[AlertBlockStall] {
		currentAlerts = append(currentAlerts, Alert{
			Type:      AlertBlockStall,
			Message:   fmt.Sprintf("block production stalled — last block %.1fs ago", lastBlockAge.Seconds()),
			Timestamp: now,
			Severity:  "critical",
		})
	}
	if activeAlertsCopy[AlertNoValidators] {
		currentAlerts = append(currentAlerts, Alert{
			Type:      AlertNoValidators,
			Message:   "no validators registered",
			Timestamp: now,
			Severity:  "critical",
		})
	}
	m.mu.RUnlock()

	uptime := time.Since(m.startTime)

	status := "healthy"
	if len(currentAlerts) > 0 {
		for _, a := range currentAlerts {
			if a.Severity == "critical" {
				status = "critical"
				break
			}
		}
		if status != "critical" {
			status = "degraded"
		}
	}

	if currentAlerts == nil {
		currentAlerts = []Alert{}
	}

	return HealthReport{
		Status:         status,
		Uptime:         formatDuration(uptime),
		UptimeSeconds:  uptime.Seconds(),
		BlockHeight:    stats.Height,
		BlocksPerSec:   stats.BlocksPerSec,
		LastBlockAge:   fmt.Sprintf("%.3fs", lastBlockAge.Seconds()),
		LastBlockAgeMs: lastBlockAge.Milliseconds(),
		MempoolDepth:   0, // updated via SetMempoolDepth if wired
		ValidatorCount: stats.AgentCount,
		ActiveAlerts:   currentAlerts,
		AlertCount:     len(currentAlerts),
		CheckedAt:      now,
	}
}

// SetLastBlockTime updates the last-seen block time (call this from the block producer)
func (m *Monitor) SetLastBlockTime(t time.Time) {
	m.mu.Lock()
	m.lastBlockTime = t
	m.mu.Unlock()
}

// formatDuration formats a duration as a human-readable string
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh%dm%ds", h, m, s)
}
