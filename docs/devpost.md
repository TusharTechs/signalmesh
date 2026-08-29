# SignalMesh — Devpost submission

**Track: Distributed Systems.** "Build something that handles failure, latency or
scale: a consensus mechanism, a real-time data pipeline, an agent orchestration
layer, or an inference-serving path." SignalMesh is an agent orchestration +
inference-serving path with a consensus mechanism at its core.

## Inspiration

AI agents now sit on top of long, fragile dependency graphs — LLM providers,
tools, MCP servers, APIs. These dependencies fail in ways ordinary infra can't
see: an HTTP 200 with a broken response contract, silent quality decay, a cost
explosion, a retry loop that burns money. Every existing health check answers
"is it up?" — the wrong question for AI.

## What it does

SignalMesh is a distributed reliability and attention control plane. Three Go
nodes gossip over NATS, reach **majority health consensus** on every provider,
and for each request decide — deterministically — whether to **accept, retry,
fall back, or escalate to a human**, emitting a machine-readable reason code at
every stage.

It detects and handles, live and on stage:

1. **Semantic failure on HTTP 200** — response contract validation.
2. **Provider outage** — circuit breaker → zero-cost local fallback, no dropped requests.
3. **SignalMesh node failure** — heartbeat timeout, observations dropped from consensus, traffic continues.
4. **Agent retry loop** — request-fingerprint detection → breaker trip → incident → escalation, runaway cost stopped.
5. **Traffic spike** — per-class bulkheads + bounded queues + load shedding protect critical traffic.
6. **Risk / economics** — tiny budget forces zero-cost fallback for low-risk work; high-risk work with no safe provider escalates instead of acting blindly.

## How we built it

- **Data plane**: Go. Admission control → loop detection → deterministic scored
  routing → circuit breaker → generation → semantic validation → quality/cost
  gates → escalation.
- **Control plane**: NATS. Heartbeats, provider-health observations, chaos
  commands, incidents, escalations. Idempotent (event IDs), order-tolerant
  (timestamps + versions).
- **Consensus**: lightweight majority quorum over recent observations from
  still-alive nodes. Explicitly *not* Raft — trade-off documented in ADR-006.
- **Chaos engine**: every failure scenario is one HTTP call that auto-restores.
- **Dashboard**: Next.js, polls aggregated node state every 2s.
- **Benchmarks**: Go load generator through the load balancer against the live
  cluster.

## What is real

The reliability control plane is real, running code with `go test -race`
coverage. The **provider upstream is a deterministic simulator** so failure
scenarios are reproducible in a 5-minute slot — it models the failure *modes*
faithfully (200-with-broken-contract, latency inflation, hard 5xx), and the
`Provider` interface is all a real OpenAI/Anthropic connector needs. Budgets and
decision history are in-memory; Postgres/Redis are provisioned for the durable
design but off the request path today. All disclosed in the README and ADRs.

## Measured results

- 1000 req @ 50 concurrent, baseline: 100% success, p95 200 ms, flat tail.
- 100 req during a **full provider outage**: **100% success**, p95 56 ms, all on the zero-cost local path.

## Challenges

Consensus that stays honest under partition without a leader; making circuit
breaker + health recovery fast enough to show live; keeping the whole demo to
"hit one endpoint, watch the dashboard."

## What's next

Real provider connectors, Redis-backed shared budget/loop state, Postgres audit
log, task-specific contracts, Raft if formal safety is needed.

## Try it

```bash
make infra && ./scripts/dev-cluster.sh && make dashboard
./scripts/demo.sh
```

> SignalMesh doesn't just keep AI running.
> It decides when AI is healthy enough to keep running without you.
