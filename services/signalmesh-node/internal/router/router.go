package router

import (
	"fmt"
	"log/slog"
	"sync"

	"signalmesh/internal/circuitbreaker"
	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
)

// HealthSource provides provider health.
type HealthSource interface {
	GetProviderHealth(name string) providers.ProviderHealth
}

// ProviderEntry binds a provider to its circuit breaker and routing metadata.
type ProviderEntry struct {
	Provider   providers.Provider
	Breaker    *circuitbreaker.Breaker
	IsFallback bool
	Priority   int
	CostUSD    float64
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
	mu           sync.RWMutex
	entries      []ProviderEntry
	healthSource HealthSource
	logger       *slog.Logger
}

func New(logger *slog.Logger, healthSource HealthSource) *Router {
	return &Router{
		healthSource: healthSource,
		logger:       logger,
	}
}

// AddProvider adds a provider with a default estimated cost.
func (r *Router) AddProvider(p providers.Provider, breaker *circuitbreaker.Breaker, isFallback bool) {
	r.AddProviderWithCost(p, breaker, isFallback, 0.0001)
}

// AddProviderWithCost adds a provider with an explicit estimated request cost.
func (r *Router) AddProviderWithCost(
	p providers.Provider,
	breaker *circuitbreaker.Breaker,
	isFallback bool,
	costUSD float64,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, ProviderEntry{
		Provider:   p,
		Breaker:    breaker,
		IsFallback: isFallback,
		Priority:   len(r.entries),
		CostUSD:    costUSD,
	})
}

// Select chooses the best provider for the request, policy, and remaining budget.
func (r *Router) Select(
	req providers.ModelRequest,
	pol policy.Policy,
	budgetRemainingUSD float64,
) (*ProviderEntry, *Decision, error) {
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
		if r.healthSource != nil {
			health = r.healthSource.GetProviderHealth(name)
		}

		if health.ProviderName == "" {
			health.ProviderName = name
		}

		if health.Status == "UNHEALTHY" {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "PROVIDER_UNHEALTHY")
			decision.Evaluations = append(decision.Evaluations, eval)
			continue
		}

		// Budget remaining enforcement.
		if budgetRemainingUSD < 0 && entry.CostUSD > 0 {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "REMAINING_BUDGET_NEGATIVE")
			decision.Evaluations = append(decision.Evaluations, eval)
			continue
		}

		if budgetRemainingUSD >= 0 && entry.CostUSD > budgetRemainingUSD {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "PROVIDER_COST_EXCEEDS_REMAINING_BUDGET")
			decision.Evaluations = append(decision.Evaluations, eval)
			continue
		}

		// Request-level budget enforcement.
		if pol.MaxCostUSD > 0 && entry.CostUSD > pol.MaxCostUSD {
			eval.Allowed = false
			eval.ReasonCodes = append(eval.ReasonCodes, "ESTIMATED_COST_EXCEEDS_REQUEST_BUDGET")
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

		// Cost component.
		if entry.CostUSD == 0 {
			score += 10
			eval.ReasonCodes = append(eval.ReasonCodes, "ZERO_COST_PROVIDER")
		} else if pol.MaxCostUSD > 0 && entry.CostUSD <= pol.MaxCostUSD {
			score += 5
			eval.ReasonCodes = append(eval.ReasonCodes, "WITHIN_REQUEST_BUDGET")
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
