# ADR-004: Why deterministic routing instead of LLM routing

Status: Accepted

Routing must be fast, testable, explainable, and safe under failure.
LLMs may reason about semantics, but deterministic code must control
retries, timeouts, budgets, routing, and coordination.

Principle: "LLMs reason. Code guarantees."