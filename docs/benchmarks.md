# Benchmarks

All numbers below are produced by `make benchmark`, which runs the Go load
generator (`cmd/signalmesh-bench`) through the load balancer against the live
3-node cluster and writes raw JSON to `docs/benchmark-results/`.

Reproduce:

```bash
make infra            # NATS
./scripts/dev-cluster.sh   # 3 nodes + load balancer
make benchmark
```

Environment: Apple Silicon laptop, 3 node processes + load balancer + NATS, all
local. The provider layer is the deterministic simulator (see the README "What
is real" section), so these figures measure **SignalMesh's own overhead and
failover behaviour**, not upstream model latency.

## Baseline (primary healthy, all traffic `mock-primary`)

| Concurrency | Requests | Success | Failed | p50 | p95 | p99 | Throughput |
|---|---|---|---|---|---|---|---|
| 1  | 20   | 20   | 0 | 185 ms | 199 ms | 201 ms | 5 req/s |
| 10 | 200  | 200  | 0 | 176 ms | 199 ms | 201 ms | 55 req/s |
| 20 | 400  | 400  | 0 | 178 ms | 199 ms | 202 ms | 109 req/s |
| 50 | 1000 | 1000 | 0 | 177 ms | 200 ms | 213 ms | 273 req/s |

The simulator adds ~150 ms + jitter per call. p95 stays flat (~199 ms) from 1 to
50 concurrent, i.e. SignalMesh's admission → routing → validation → circuit-breaker
pipeline adds no measurable tail latency at these levels.

## Failover under full provider outage

Scenario `provider-outage` active, same load through the load balancer:

| Requests | Success | Failed | p50 | p95 | p99 | Throughput | Served by |
|---|---|---|---|---|---|---|---|
| 100 | 100 | 0 | 53 ms | 56 ms | 56 ms | 184 req/s | `local-fallback` (100%) |

**Zero failed requests** during a total primary outage. Latency *drops* because
the circuit is open (no wasted attempt on the dead primary) and the zero-cost
local path answers directly.

## Raw data

`docs/benchmark-results/*.json` — one file per run, emitted verbatim by the load
generator. Regenerated 2026-08-29.
