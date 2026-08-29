# Architecture

## Planes

1. **Data plane** (Go): proxy, admission, policy, router, validator, circuit
   breakers, budgets, loop detector, escalation. Deterministic and in the
   request path.
2. **Control / event plane** (NATS): node heartbeats, provider-health
   observations, chaos commands, incidents, escalations. Idempotent
   (event IDs) and order-tolerant (timestamps + versions).
3. **State plane**: in-memory node-local store today (cluster state, budgets,
   loop state, decision log, incidents). PostgreSQL (durable audit) and Redis
   (shared fast state) are provisioned in compose for the intended design but
   are **not on the request path in this prototype** — see `docs/decisions.md`.

## Component responsibilities

`services/signalmesh-node/internal/`:

- `proxy` — HTTP ingress, deadlines, bounded retries, failure classification, reason codes.
- `admission` — traffic classification, per-class bulkheads, bounded queues, backpressure, global load shedding.
- `policy` — normalize request risk/budget/latency into an internal policy.
- `router` — deterministic scored provider selection using consensus health, latency budget, cost, contract-failure rate, and risk.
- `validator` — semantic response contracts (schema, required fields, confidence thresholds, length).
- `circuitbreaker` — explicit CLOSED / OPEN / HALF_OPEN state machine per provider, plus forced `Trip()` on agent incidents.
- `cluster` — heartbeats, provider-health observation propagation, majority-quorum consensus, idempotency, node liveness, chaos fan-out, heartbeat enable/disable (simulated node failure).
- `health` — active provider probing on a bounded time window (fast, visible recovery).
- `loopdetector` — request-fingerprint failure counting → agent-loop detection.
- `budget` — request / agent / global cost governance.
- `escalation` — decides when a failure needs a human; emits an explainable escalation with a recommended action.
- `incident` — machine-readable incident records, published to NATS.
- `chaos` — single-endpoint, auto-restoring failure scenarios (see `scenarios/README.md`).
- `observability` — Prometheus metrics, bounded decision log, HTTP middleware.

## Consensus (ADR-006)

Majority quorum over recent, still-alive-node observations. Not Raft: no leader,
no log replication, no formal safety under partition. Under partition each node
falls back to its own observations and makes no global claims. Chosen for
explainability in a one-day build; the trade-off is documented, not hidden.

## Endpoints

- `POST /v1/chat/completions` — the AI request path.
- `GET /api/dashboard` — aggregated live state for the UI.
- `GET /metrics` — Prometheus text format.
- `GET /api/decisions` — recent explainable decisions.
- `POST /debug/chaos/scenario` — run/restore a chaos scenario.
- `GET /debug/{cluster,provider-health,circuit,budget,incidents,escalations,admission}` — inspection.
