package router

import (
	"context"
	"testing"

	"signalmesh/internal/circuitbreaker"
	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
)

type stubProvider struct {
	name   string
	health providers.ProviderHealth
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Generate(context.Context, providers.ModelRequest) (*providers.ModelResponse, error) {
	return &providers.ModelResponse{ProviderName: s.name, Content: `{"answer":"ok","confidence":0.9}`}, nil
}
func (s *stubProvider) GetHealth() providers.ProviderHealth { return s.health }

type stubHealth map[string]providers.ProviderHealth

func (h stubHealth) GetProviderHealth(name string) providers.ProviderHealth { return h[name] }

func healthy(name string) providers.ProviderHealth {
	return providers.ProviderHealth{ProviderName: name, Status: "HEALTHY", AvailabilityPct: 100, P95LatencyMs: 150}
}

func newTestRouter(health stubHealth) *Router {
	r := New(nil, health)
	primary := &stubProvider{name: "primary", health: health["primary"]}
	local := &stubProvider{name: "local", health: health["local"]}
	r.AddProviderWithCost(primary, circuitbreaker.New(circuitbreaker.DefaultConfig()), false, 0.0001)
	r.AddProviderWithCost(local, circuitbreaker.New(circuitbreaker.DefaultConfig()), true, 0.0)
	return r
}

func TestRouterPrefersHealthyPrimary(t *testing.T) {
	r := newTestRouter(stubHealth{"primary": healthy("primary"), "local": healthy("local")})

	req := providers.ModelRequest{TaskType: "qa", RiskLevel: "low"}
	entry, decision, err := r.Select(req, policy.FromRequest(req), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Provider.Name() != "primary" {
		t.Fatalf("expected primary, got %s (%v)", entry.Provider.Name(), decision.ReasonCodes)
	}
	if decision.FallbackUsed {
		t.Fatal("did not expect fallback")
	}
}

func TestRouterFailsOverWhenPrimaryUnhealthy(t *testing.T) {
	h := stubHealth{"primary": healthy("primary"), "local": healthy("local")}
	h["primary"] = providers.ProviderHealth{ProviderName: "primary", Status: "UNHEALTHY", AvailabilityPct: 0}
	r := newTestRouter(h)

	req := providers.ModelRequest{TaskType: "qa", RiskLevel: "low"}
	entry, decision, err := r.Select(req, policy.FromRequest(req), 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Provider.Name() != "local" || !decision.FallbackUsed {
		t.Fatalf("expected local fallback, got %s fallback=%v", entry.Provider.Name(), decision.FallbackUsed)
	}
}

func TestRouterHighRiskRefusesLocalFallback(t *testing.T) {
	h := stubHealth{
		"primary": {ProviderName: "primary", Status: "UNHEALTHY", AvailabilityPct: 0},
		"local":   healthy("local"),
	}
	r := newTestRouter(h)

	req := providers.ModelRequest{TaskType: "financial_action", RiskLevel: "high"}
	_, decision, err := r.Select(req, policy.FromRequest(req), 1.0)
	if err == nil {
		t.Fatalf("expected no-provider error for high-risk task, got %v", decision)
	}
}

func TestRouterRejectsWhenBudgetNegative(t *testing.T) {
	r := newTestRouter(stubHealth{"primary": healthy("primary"), "local": healthy("local")})

	req := providers.ModelRequest{TaskType: "qa", RiskLevel: "low"}
	// Negative remaining budget: only the zero-cost fallback may run.
	entry, _, err := r.Select(req, policy.FromRequest(req), -1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Provider.Name() != "local" {
		t.Fatalf("expected zero-cost local provider under negative budget, got %s", entry.Provider.Name())
	}
}
