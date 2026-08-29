package router

import (
	"fmt"
	"log/slog"
	"sync"

	"signalmesh/internal/circuitbreaker"
	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
)

// ProviderEntry binds a provider to its circuit breaker and routing metadata.
type ProviderEntry struct {
	Provider   providers.Provider
	Breaker    *circuitbreaker.Breaker
	IsFallback bool
	Priority   int
}

// ProviderEvaluation explains how a provider was scored during routing.
type ProviderEvaluation struct {
	Provider    string   `json:"provider"`
	Allowed     bool     `json:"allowed"`
	Score       float64  `json:"score"`
	ReasonCodes []string `json:"reason_codes"`
}

// Decision explains why SignalMesh selected a provider or failed to select one.
type Decision struct {
	SelectedProvider string               `json:"selected_provider"`
	FallbackUsed     bool                 `json:"fallback_used"`
	ReasonCodes      []string             `json:"reason_codes"`
	Evaluations      []ProviderEvaluation `json:"evaluations"`
}

// Router selects providers using deterministic policy-driven scoring.
type Router struct {
	mu      sync.RWMutex
	entries []ProviderEntry
	logger  *slog.Logger
}

func New(logger *slog.Logger) *Router {
	return &Router{
		logger: logger,
	}
}

func (r *Router) AddProvider(p providers.Provider, breaker *circuitbreaker.Breaker, isFallback bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, ProviderEntry{
		Provider:   p,
		Breaker:    breaker,
		IsFallback: isFallback,
		Priority:   len(r.entries),
	})
}

// Select chooses the best provider for the request and policy.
func (r *Router) Select(req providers.ModelRequest, pol policy.Policy) (*ProviderEntry, *Decision, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	decision := &Decision{}

	if len(r.entries) == 0 {
		decision.ReasonCodes = []string{"NO_PROVIDERS_REGISTERED"}
		return nil, decision, fmt.Errorf("no providers registered")
	}

	bestIdx := -1
	bestScore := -1e9

	for i := range r.entries {
		entry := r.entries[i]
		name := entry.Provider.Name()

		eval := ProviderEvaluation{
			Provider:    name,
			Allowed:     true,
			ReasonCodes: []string{},
		}

		if !entry.Breaker.Available() {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "CIRCUIT_NOT_AVAILABLE")
			decision.Evaluations = append(decision.Evaluations, eval)
			continue
		}

		if entry.IsFallback && !pol.AllowLocalFallback {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "FALLBACK_NOT_ALLOWED_BY_POLICY")
			decision.Evaluations = append(decision.Evaluations, eval)
			continue
		}

		health := entry.Provider.GetHealth()

		if health.Status == "UNHEALTHY" {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "PROVIDER_UNHEALTHY")
			decision.Evaluations = append(decision.Evaluations, eval)
			continue
		}

		score := 0.0

		availability := health.AvailabilityPct / 100.0
		if availability < 0 {
			availability = 0
		}
		if availability > 1 {
			availability = 1
		}

		// Availability component.
		score += availability * 40

		// Latency component.
		if pol.MaxLatencyMs > 0 && health.P95LatencyMs > pol.MaxLatencyMs {
			score -= 20
			eval.ReasonCodes = append(eval.ReasonCodes, "P95_LATENCY_EXCEEDED")
		} else {
			score += 20
		}

		// Health status component.
		switch health.Status {
		case "HEALTHY":
			score += 25
		case "DEGRADED":
			score += 5
			eval.ReasonCodes = append(eval.ReasonCodes, "PROVIDER_DEGRADED")
		default:
			eval.ReasonCodes = append(eval.ReasonCodes, "PROVIDER_HEALTH_UNKNOWN")
		}

		// Contract failure penalty.
		if health.ContractFailurePct > 0 {
			score -= health.ContractFailurePct * 0.4

			if health.ContractFailurePct > 10 {
				eval.ReasonCodes = append(eval.ReasonCodes, "CONTRACT_FAILURE_RATE_HIGH")
			}
		}

		// Fallback penalty.
		if entry.IsFallback {
			score -= 5

			if pol.RiskLevel == policy.RiskHigh {
				score -= 15
				eval.ReasonCodes = append(eval.ReasonCodes, "HIGH_RISK_FALLBACK_PENALTY")
			}
		}

		eval.Score = score
		decision.Evaluations = append(decision.Evaluations, eval)

		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestIdx == -1 {
		decision.ReasonCodes = []string{"NO_PROVIDER_AVAILABLE"}
		return nil, decision, fmt.Errorf("no provider available for request policy")
	}

	bestEntry := r.entries[bestIdx]
	bestEval := decision.Evaluations[bestIdx]

	decision.SelectedProvider = bestEntry.Provider.Name()
	decision.FallbackUsed = bestEntry.IsFallback
	decision.ReasonCodes = append([]string{"ROUTING_SCORE_SELECTED"}, bestEval.ReasonCodes...)

	return &bestEntry, decision, nil
}
