# Competitive positioning (honest)

Traditional gateways (Kong, Envoy): network/API health, routing.

LLM gateways (LiteLLM, Portkey, OpenRouter, Envoy AI Gateway): provider
routing, retries, fallbacks, budgets, rate limits.

SignalMesh adds:

- distributed provider-health consensus across nodes,
- semantic contract health as a first-class signal,
- agent-loop detection,
- human attention escalation,
- explainable routing decisions,
- failure injection as a product feature.

We do not claim competitors "cannot" do these; we claim SignalMesh is
built specifically around distributed trust-aware AI reliability.