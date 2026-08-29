package observability

import (
	"sync"
	"time"
)

// DecisionRecord is an explainable record of one AI request outcome.
type DecisionRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	RequestID    string    `json:"request_id"`
	AgentID      string    `json:"agent_id"`
	TaskType     string    `json:"task_type"`
	RiskLevel    string    `json:"risk_level"`
	TrafficClass string    `json:"traffic_class"`
	Provider     string    `json:"provider"`
	FallbackUsed bool      `json:"fallback_used"`
	Status       int       `json:"status"`
	Outcome      string    `json:"outcome"`
	ReasonCodes  []string  `json:"reason_codes"`
	LatencyMs    int64     `json:"latency_ms"`
}

// DecisionLog keeps recent request decisions for debugging and dashboarding.
type DecisionLog struct {
	mu    sync.RWMutex
	items []DecisionRecord
	limit int
}

// NewDecisionLog creates a bounded decision log.
func NewDecisionLog(limit int) *DecisionLog {
	if limit <= 0 {
		limit = 100
	}

	return &DecisionLog{
		items: make([]DecisionRecord, 0, limit),
		limit: limit,
	}
}

// Add appends a decision record.
func (d *DecisionLog) Add(record DecisionRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.items = append(d.items, record)

	if len(d.items) > d.limit {
		d.items = d.items[len(d.items)-d.limit:]
	}
}

// Recent returns the newest records first.
func (d *DecisionLog) Recent(n int) []DecisionRecord {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if n <= 0 || len(d.items) == 0 {
		return []DecisionRecord{}
	}

	start := len(d.items) - n
	if start < 0 {
		start = 0
	}

	out := make([]DecisionRecord, 0, len(d.items)-start)

	for i := len(d.items) - 1; i >= start; i-- {
		out = append(out, d.items[i])
	}

	return out
}

// List returns all stored records oldest first.
func (d *DecisionLog) List() []DecisionRecord {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]DecisionRecord, len(d.items))
	copy(out, d.items)

	return out
}
