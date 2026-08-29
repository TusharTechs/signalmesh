package providers

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// MockProvider simulates an AI provider.
// It will later be controlled by the Chaos Engine.
type MockProvider struct {
	name   string
	logger *slog.Logger

	mu            sync.RWMutex
	latencyBaseMs int
	errorRate     float64
	contractFail  bool

	totalReqs      int
	successReqs    int
	totalLatencyMs int64
}

// NewMockProvider creates a healthy mock provider by default.
func NewMockProvider(name string, logger *slog.Logger) *MockProvider {
	return &MockProvider{
		name:          name,
		logger:        logger,
		latencyBaseMs: 150,
		errorRate:     0.0,
		contractFail:  false,
	}
}

// Name returns the provider name.
func (m *MockProvider) Name() string {
	return m.name
}

// InjectFailure allows deterministic failure injection.
// This will be used by the Chaos Engine.
func (m *MockProvider) InjectFailure(latencyMs int, errorRate float64, contractFail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.latencyBaseMs = latencyMs
	m.errorRate = errorRate
	m.contractFail = contractFail

	m.logger.Warn(
		"Mock provider failure injected",
		"provider", m.name,
		"latency_ms", latencyMs,
		"error_rate", errorRate,
		"contract_fail", contractFail,
	)
}

// Generate simulates model generation.
func (m *MockProvider) Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error) {
	start := time.Now()

	m.mu.RLock()
	latency := m.latencyBaseMs
	errRate := m.errorRate
	contractFail := m.contractFail
	m.mu.RUnlock()

	// Simulate model/network latency while respecting cancellation/deadlines.
	jitter := rand.Intn(50)
	delay := time.Duration(latency+jitter) * time.Millisecond

	select {
	case <-time.After(delay):
		// Continue normally.
	case <-ctx.Done():
		m.recordRequest(false, time.Since(start))
		return nil, ctx.Err()
	}

	// Simulate hard provider failure.
	if rand.Float64() < errRate {
		m.recordRequest(false, time.Since(start))
		return nil, fmt.Errorf("mock provider internal server error")
	}

	// Simulate semantic/contract failure.
	// HTTP can still be 200 while the response violates the contract.
	content := `{"answer":"The capital of France is Paris.","confidence":0.98}`
	if contractFail {
		content = `{"answer":"Paris"}`
	}

	resp := &ModelResponse{
		ProviderName:  m.name,
		Model:         req.Model,
		Content:       content,
		FinishReason:  "stop",
		InputTokens:   len(req.Messages) * 10,
		OutputTokens:  20,
		LatencyMs:     time.Since(start).Milliseconds(),
		EstimatedCost: 0.0001,
	}

	m.recordRequest(true, time.Since(start))
	return resp, nil
}

// GetHealth returns the current local health observation.
func (m *MockProvider) GetHealth() ProviderHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := "HEALTHY"
	availPct := 100.0

	if m.totalReqs > 0 {
		availPct = (float64(m.successReqs) / float64(m.totalReqs)) * 100.0
	}

	if availPct < 90.0 {
		status = "DEGRADED"
	}

	if availPct < 50.0 {
		status = "UNHEALTHY"
	}

	avgLatency := int64(0)
	if m.totalReqs > 0 {
		avgLatency = m.totalLatencyMs / int64(m.totalReqs)
	}

	return ProviderHealth{
		ProviderName:       m.name,
		Status:             status,
		AvailabilityPct:    availPct,
		P95LatencyMs:       avgLatency,
		ContractFailurePct: 0.0,
		LastUpdated:        time.Now(),
		Version:            1,
	}
}

func (m *MockProvider) recordRequest(success bool, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totalReqs++
	if success {
		m.successReqs++
	}

	m.totalLatencyMs += duration.Milliseconds()
}
