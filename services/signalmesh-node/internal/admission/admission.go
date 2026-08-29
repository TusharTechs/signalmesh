package admission

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
)

type Class string

const (
	ClassCritical   Class = "CRITICAL"
	ClassNormal     Class = "NORMAL"
	ClassBackground Class = "BACKGROUND"
)

// Config defines bulkhead capacity for one traffic class.
type Config struct {
	Concurrency int
	MaxQueue    int
}

type classState struct {
	cfg      Config
	sem      chan struct{}
	waiters  atomic.Int64
	admitted atomic.Int64
	dropped  atomic.Int64
}

// Manager provides node-local admission control.
//
// It implements:
// - bulkhead isolation per traffic class
// - bounded queues
// - backpressure
// - load shedding
type Manager struct {
	globalMax    int64
	totalActive  atomic.Int64
	totalDropped atomic.Int64

	mu      sync.RWMutex
	classes map[Class]*classState
}

func normalizeConfig(cfg Config) Config {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}

	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 0
	}

	return cfg
}

func newClassState(cfg Config) *classState {
	cfg = normalizeConfig(cfg)

	return &classState{
		cfg: cfg,
		sem: make(chan struct{}, cfg.Concurrency),
	}
}

// NewManager creates an admission manager.
func NewManager(critical Config, normal Config, background Config, globalMax int) *Manager {
	m := &Manager{
		globalMax: int64(globalMax),
		classes:   make(map[Class]*classState),
	}

	m.classes[ClassCritical] = newClassState(critical)
	m.classes[ClassNormal] = newClassState(normal)
	m.classes[ClassBackground] = newClassState(background)

	return m
}

func (m *Manager) classStateFor(class Class) *classState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.classes[class]
	if !ok {
		return m.classes[ClassNormal]
	}

	return state
}

// Acquire attempts to admit a request.
func (m *Manager) Acquire(ctx context.Context, class Class) (bool, []string) {
	state := m.classStateFor(class)

	// Global load shedding.
	// Critical traffic is preserved even when the node is near capacity.
	if m.globalMax > 0 && class != ClassCritical && m.totalActive.Load() >= m.globalMax {
		state.dropped.Add(1)
		m.totalDropped.Add(1)

		return false, []string{
			"GLOBAL_LOAD_SHEDDING",
			"NON_CRITICAL_TRAFFIC_SHED",
		}
	}

	// Bounded queue.
	if state.waiters.Load() >= int64(state.cfg.MaxQueue) {
		state.dropped.Add(1)
		m.totalDropped.Add(1)

		return false, []string{
			"ADMISSION_QUEUE_FULL",
		}
	}

	state.waiters.Add(1)
	defer state.waiters.Add(-1)

	select {
	case state.sem <- struct{}{}:
		state.admitted.Add(1)
		m.totalActive.Add(1)
		return true, nil

	case <-ctx.Done():
		reason := "ADMISSION_TIMEOUT"
		if ctx.Err() == context.Canceled {
			reason = "ADMISSION_CANCELED"
		}

		return false, []string{reason}
	}
}

// Release releases a previously admitted request.
func (m *Manager) Release(class Class) {
	state := m.classStateFor(class)

	select {
	case <-state.sem:
		m.totalActive.Add(-1)
	default:
		// This should not happen if Release is only called after Acquire.
	}
}

// Status returns admission metrics for debugging.
func (m *Manager) Status() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	classes := make(map[string]interface{})

	for name, state := range m.classes {
		classes[string(name)] = map[string]interface{}{
			"concurrency_limit": state.cfg.Concurrency,
			"queue_limit":       state.cfg.MaxQueue,
			"active":            len(state.sem),
			"waiters":           state.waiters.Load(),
			"admitted":          state.admitted.Load(),
			"dropped":           state.dropped.Load(),
		}
	}

	return map[string]interface{}{
		"global_max_active": m.globalMax,
		"total_active":      m.totalActive.Load(),
		"total_dropped":     m.totalDropped.Load(),
		"classes":           classes,
	}
}

// Classify maps a request to a traffic class.
func Classify(req providers.ModelRequest, pol policy.Policy) Class {
	priority := strings.ToLower(strings.TrimSpace(req.Priority))

	switch priority {
	case "critical":
		return ClassCritical
	case "background":
		return ClassBackground
	case "normal":
		return ClassNormal
	}

	if pol.RiskLevel == policy.RiskHigh {
		return ClassCritical
	}

	task := strings.ToLower(strings.TrimSpace(req.TaskType))

	backgroundKeywords := []string{
		"batch",
		"index",
		"indexing",
		"analytics",
		"offline",
		"background",
		"cleanup",
		"reindex",
	}

	for _, keyword := range backgroundKeywords {
		if strings.Contains(task, keyword) {
			return ClassBackground
		}
	}

	return ClassNormal
}
