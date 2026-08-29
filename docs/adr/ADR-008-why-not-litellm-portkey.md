# ADR-008: Why not simply use LiteLLM/Portkey?

Status: Accepted

SignalMesh is not attempting to replace mature general-purpose LLM
gateways. LiteLLM/Portkey/OpenRouter provide routing, retries, fallbacks,
and budgets.

SignalMesh explores a different problem: distributed trust-aware
reliability coordination across independent nodes, including semantic
contract health, provider-health consensus, agent-loop detection, and
human attention escalation.