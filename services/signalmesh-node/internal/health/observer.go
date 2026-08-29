package health

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"signalmesh/internal/cluster"
	"signalmesh/internal/providers"
	"signalmesh/internal/validator"
)

type probeResult struct {
	timestamp       time.Time
	success         bool
	contractFailure bool
	latencyMs       int64
}

// Observer actively probes providers and publishes local health observations.
//
// For the hackathon prototype, active probes make failure detection visible
// even when user traffic is low. In production, this would be combined with
// passive observation from real requests.
type Observer struct {
	nodeID    string
	store     *cluster.Store
	providers []providers.Provider
	logger    *slog.Logger
	interval  time.Duration

	mu      sync.RWMutex
	results map[string][]probeResult
}

// NewObserver creates a health observer.
func NewObserver(
	nodeID string,
	store *cluster.Store,
	providerList []providers.Provider,
	logger *slog.Logger,
) *Observer {
	return &Observer{
		nodeID:    nodeID,
		store:     store,
		providers: providerList,
		logger:    logger,
		interval:  1500 * time.Millisecond,
		results:   make(map[string][]probeResult),
	}
}

// Start launches the observer loop.
func (o *Observer) Start(ctx context.Context) {
	go o.loop(ctx)
}

func (o *Observer) loop(ctx context.Context) {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	// Probe immediately.
	o.probeAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.probeAll(ctx)
		}
	}
}

func (o *Observer) probeAll(ctx context.Context) {
	for _, provider := range o.providers {
		health := o.probeProvider(ctx, provider)

		o.store.SetLocalProviderHealth(provider.Name(), health)

		o.store.PublishProviderHealth(cluster.ProviderHealthObservation{
			Provider:           provider.Name(),
			Status:             health.Status,
			AvailabilityPct:    health.AvailabilityPct,
			P95LatencyMs:       health.P95LatencyMs,
			ContractFailurePct: health.ContractFailurePct,
		})
	}
}

func (o *Observer) probeProvider(
	ctx context.Context,
	provider providers.Provider,
) providers.ProviderHealth {
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()

	start := time.Now()

	req := providers.ModelRequest{
		RequestID: fmt.Sprintf("probe-%s-%d", provider.Name(), time.Now().UnixNano()),
		Messages: []providers.Message{
			{
				Role:    "user",
				Content: "health probe",
			},
		},
		Model:     "probe",
		TaskType:  "probe",
		RiskLevel: "low",
		AgentID:   "signalmesh-health-observer",
	}

	resp, err := provider.Generate(probeCtx, req)
	latency := time.Since(start).Milliseconds()

	success := false
	contractFailure := false

	if err == nil && resp != nil {
		validation := validator.Validate(resp.Content, validator.DefaultContract())
		if validation.Valid {
			success = true
		} else {
			contractFailure = true
		}
	}

	o.record(provider.Name(), probeResult{
		timestamp:       time.Now(),
		success:         success,
		contractFailure: contractFailure,
		latencyMs:       latency,
	})

	return o.computeHealth(provider.Name())
}

func (o *Observer) record(provider string, result probeResult) {
	o.mu.Lock()
	defer o.mu.Unlock()

	results := o.results[provider]
	results = append(results, result)

	// Keep only the last 20 probes.
	if len(results) > 20 {
		results = results[len(results)-20:]
	}

	o.results[provider] = results
}

// healthWindow bounds how far back probe history influences the current health
// verdict. It keeps recovery after a resolved incident fast and visible on
// stage (roughly one window of clean probes) instead of waiting for a large
// ring buffer to flush.
const healthWindow = 15 * time.Second

func (o *Observer) computeHealth(provider string) providers.ProviderHealth {
	now := time.Now()

	o.mu.RLock()
	all := o.results[provider]
	results := make([]probeResult, 0, len(all))
	for _, r := range all {
		if now.Sub(r.timestamp) <= healthWindow {
			results = append(results, r)
		}
	}
	o.mu.RUnlock()

	if len(results) == 0 {
		return providers.ProviderHealth{
			ProviderName:    provider,
			Status:          "UNKNOWN",
			AvailabilityPct: 100,
			LastUpdated:     now,
		}
	}

	total := len(results)
	successes := 0
	contractFailures := 0
	latencies := make([]int64, 0, total)

	for _, result := range results {
		if result.success {
			successes++
		}

		if result.contractFailure {
			contractFailures++
		}

		latencies = append(latencies, result.latencyMs)
	}

	availability := float64(successes) / float64(total) * 100.0
	contractFailurePct := float64(contractFailures) / float64(total) * 100.0

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p95 := latencies[len(latencies)-1]

	idx := int(float64(len(latencies)) * 0.95)
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}
	if idx >= 0 {
		p95 = latencies[idx]
	}

	status := "HEALTHY"

	if availability < 50 || contractFailurePct > 20 {
		status = "UNHEALTHY"
	} else if availability < 95 || contractFailurePct > 2 || p95 > 1500 {
		status = "DEGRADED"
	}

	return providers.ProviderHealth{
		ProviderName:       provider,
		Status:             status,
		AvailabilityPct:    availability,
		P95LatencyMs:       p95,
		ContractFailurePct: contractFailurePct,
		LastUpdated:        now,
	}
}
