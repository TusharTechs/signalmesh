package budget

import (
	"math"
	"sync"
)

// Status describes budget state for debugging.
type Status struct {
	GlobalLimit     float64 `json:"global_limit_usd"`
	GlobalSpent     float64 `json:"global_spent_usd"`
	GlobalRemaining float64 `json:"global_remaining_usd"`
	AgentLimit      float64 `json:"agent_limit_usd"`
	AgentSpent      float64 `json:"agent_spent_usd"`
	AgentRemaining  float64 `json:"agent_remaining_usd"`
}

// Manager tracks simple in-memory budgets.
//
// This is node-local in the hackathon prototype. A production system would
// use Redis or another shared fast-state store.
type Manager struct {
	mu                sync.RWMutex
	globalLimit       float64
	globalSpent       float64
	defaultAgentLimit float64
	agentLimits       map[string]float64
	agentSpent        map[string]float64
}

// NewManager creates a budget manager.
// Limits <= 0 are treated as unlimited.
func NewManager(globalLimit float64, defaultAgentLimit float64) *Manager {
	return &Manager{
		globalLimit:       globalLimit,
		defaultAgentLimit: defaultAgentLimit,
		agentLimits:       make(map[string]float64),
		agentSpent:        make(map[string]float64),
	}
}

func effectiveLimit(limit float64) float64 {
	if limit <= 0 {
		return math.MaxFloat64
	}

	return limit
}

func minFloat(a float64, b float64) float64 {
	if a < b {
		return a
	}

	return b
}

// SetAgentLimit sets a specific limit for an agent.
func (m *Manager) SetAgentLimit(agentID string, limit float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.agentLimits[agentID] = limit
}

// Remaining returns the minimum remaining budget for the agent and global pool.
func (m *Manager) Remaining(agentID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	globalRemaining := effectiveLimit(m.globalLimit) - m.globalSpent

	agentLimit := m.defaultAgentLimit
	if limit, ok := m.agentLimits[agentID]; ok {
		agentLimit = limit
	}

	agentRemaining := effectiveLimit(agentLimit) - m.agentSpent[agentID]

	return minFloat(globalRemaining, agentRemaining)
}

// Record records actual spend after a successful request.
func (m *Manager) Record(agentID string, cost float64) {
	if cost < 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.globalSpent += cost
	m.agentSpent[agentID] += cost
}

// Status returns budget state for one agent.
func (m *Manager) Status(agentID string) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	globalLimit := effectiveLimit(m.globalLimit)
	globalRemaining := globalLimit - m.globalSpent

	agentLimit := effectiveLimit(m.defaultAgentLimit)
	if limit, ok := m.agentLimits[agentID]; ok {
		agentLimit = effectiveLimit(limit)
	}

	agentSpent := m.agentSpent[agentID]
	agentRemaining := agentLimit - agentSpent

	return Status{
		GlobalLimit:     globalLimit,
		GlobalSpent:     m.globalSpent,
		GlobalRemaining: globalRemaining,
		AgentLimit:      agentLimit,
		AgentSpent:      agentSpent,
		AgentRemaining:  agentRemaining,
	}
}
