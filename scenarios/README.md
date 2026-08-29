# Chaos scenario catalog

Every scenario is triggered by a **single** HTTP call and **auto-restores** after
its duration. No terminal juggling, no manual process kills.

```bash
curl -s -X POST http://localhost:8080/debug/chaos/scenario \
  -H 'Content-Type: application/json' \
  -d '{"scenario":"<name>","duration_seconds":45}'

# stop early / reset everything
curl -s -X POST http://localhost:8080/debug/chaos/scenario -d '{"scenario":"restore"}'
```

`make chaos SCENARIO=<name> DURATION=45` wraps the same call.

| Scenario | What it injects | What SignalMesh does | Watch for (reason codes / dashboard) |
|---|---|---|---|
| `provider-outage` | `mock-primary` returns errors at 100% | Circuit opens, routes to zero-cost local fallback, keeps serving | `CIRCUIT_NOT_AVAILABLE`, `ZERO_COST_PROVIDER`, `fallback_total` rises |
| `semantic-degradation` | `mock-primary` returns **HTTP 200** with a contract-violating body | Marks provider health `UNHEALTHY` on contract-failure rate, reroutes | `SEMANTIC_VALIDATION_FAILED`, `CONTRACT_FAILURE_RATE_HIGH`, `semantic_failures_total` |
| `latency-spike` | `mock-primary` p95 → ~2000 ms | Latency-budget penalty in routing score, prefers faster provider | `P95_LATENCY_EXCEEDED` |
| `node-failure` | Target node stops emitting heartbeats (process stays up) | Cluster ages the node out, drops its observations from consensus, LB keeps serving | node shows `DEAD` in dashboard, `successful` count keeps climbing |
| `traffic-spike` | 120-worker background load burst against the node | Bulkhead isolation + bounded queues + global load shedding protect critical traffic | `GLOBAL_LOAD_SHEDDING`, `ADMISSION_QUEUE_FULL`, `admission_dropped_total` |
| `agent-loop` | One agent retries an identical failing high-risk call | Loop detection trips the breaker, opens an incident, escalates to a human | `AGENT_LOOP_DETECTED`, `RUNAWAY_RETRY_STOPPED`, `HUMAN_ATTENTION_REQUIRED` |
| `budget-exhaustion` | Set a tiny per-agent budget (see below) | Low-risk → zero-cost fallback; high-risk with no safe provider → human escalation, never a blind action | `BUDGET_EXHAUSTED`, `HUMAN_REVIEW_BLOCK_AUTOMATIC_ACTION` |

## budget-exhaustion trigger

Budget is per-agent state, so it is driven directly rather than through the chaos
engine:

```bash
curl -s -X POST http://localhost:8080/debug/budget/set \
  -H 'Content-Type: application/json' \
  -d '{"agent_id":"budget-agent","limit_usd":0.00005}'
```

Then send one `risk_level:"low"` and one `risk_level:"high"` request as
`budget-agent` and compare the outcomes.

## Notes on realism

- Providers are a **deterministic simulator** (`internal/providers/mock.go`) plus
  an optional local Ollama path. The failure *modes* (200-with-broken-contract,
  latency inflation, hard errors) are modelled faithfully; the upstream is not a
  real OpenAI/Anthropic endpoint. See the README "What is real" section.
- `node-failure` via heartbeat-stop is the demo-friendly path. A hard
  `kill -9 <node pid>` produces the **identical** cluster reaction and is worth
  showing if a judge asks.
