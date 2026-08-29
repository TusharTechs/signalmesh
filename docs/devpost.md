# SignalMesh — Devpost project story

*Copy everything below the horizontal rule into "About the project". Devpost
renders Markdown, and LaTeX via `\\( ... \\)` inline and `$$ ... $$` displayed.*

---

## Inspiration

Every health check ever written asks the same question: **is the service up?**

That question is useless for AI. I kept running into a failure that no monitoring
I had would catch — a model provider returns **HTTP 200**, the request is
"successful" by every metric on the dashboard, and the body is unusable. A broken
JSON contract. A confidence score below the threshold. A truncated answer. The
agent downstream doesn't know, so it retries. And retries. And the first signal
anyone gets is the bill at the end of the month.

The infrastructure we have was built for services that fail loudly. AI
dependencies fail *quietly, expensively, and semantically*. So the question I
wanted to answer was different:

> Is this dependency healthy enough for **this** request, at **this** cost, with
> **this** level of risk?

## What it does

SignalMesh is a distributed reliability and attention control plane for AI agents.

Three independent Go nodes gossip over NATS, reach **majority consensus** on
every provider's health, and decide — deterministically, per request — whether to
**accept, retry, fall back, or escalate to a human**. Every decision emits a
machine-readable reason code, so nothing is a black box.

| Failure | What SignalMesh does |
|---|---|
| **HTTP 200 with a broken contract** | Semantic validation marks it a failure and reroutes |
| **Total provider outage** | Circuit opens, traffic moves to a zero-cost local path |
| **A SignalMesh node dies** | Heartbeat timeout ages it out of quorum, traffic continues |
| **Agent stuck in a retry loop** | Fingerprint detection trips the breaker, opens an incident, escalates |
| **Traffic spike** | Per-class bulkheads shed non-critical load, critical capacity untouched |
| **Budget exhausted on a high-risk task** | Refuses to guess — escalates to a human instead |

Every response carries its own explanation:

```json
{
  "phase": "agent_loop",
  "reason_codes": [
    "AGENT_LOOP_DETECTED",
    "RUNAWAY_RETRY_STOPPED",
    "HUMAN_ATTENTION_REQUIRED"
  ],
  "escalation": {
    "recommended_action": "HUMAN_REVIEW_BLOCK_AUTOMATIC_ACTION"
  }
}
```

## How routing actually decides

Routing is deterministic and auditable — no model in the control path. For a
cluster of \\( n \\) nodes, a provider's health verdict requires a simple majority
of observations from nodes that are still alive:

$$ \text{quorum}(n) = \left\lfloor \frac{n}{2} \right\rfloor + 1 $$

A dead node's observations are dropped the moment its heartbeat expires, so stale
votes never prop up a failing provider. Each candidate provider \\( p \\) is then
scored:

$$ S(p) = 40\,a_p \;+\; L_p \;+\; H_p \;-\; 0.4\,c_p \;+\; K_p \;-\; F_p $$

where \\( a_p \in [0,1] \\) is consensus availability, \\( c_p \\) is the contract-failure
percentage, and the remaining terms are policy-driven:

$$ L_p = \begin{cases} +20 & \text{p95} \le \text{latency budget} \\\\ -20 & \text{otherwise} \end{cases}
\qquad
H_p = \begin{cases} 25 & \text{HEALTHY} \\\\ 5 & \text{DEGRADED} \\\\ 0 & \text{UNKNOWN} \end{cases} $$

\\( K_p \\) rewards staying inside budget (\\( +10 \\) for a zero-cost provider,
\\( +5 \\) for one within the request budget), and \\( F_p \\) penalises fallbacks
(\\( 5 \\), rising to \\( 20 \\) when the task is high-risk — a fallback is capacity,
not a substitute for a trusted provider).

The health verdict itself is a threshold function over the probe window:

$$ \text{status} = \begin{cases}
\text{UNHEALTHY} & a < 50\% \;\lor\; c > 20\% \\\\
\text{DEGRADED}  & a < 95\% \;\lor\; c > 2\% \;\lor\; \text{p95} > 1500\,\text{ms} \\\\
\text{HEALTHY}   & \text{otherwise}
\end{cases} $$

Because it's arithmetic rather than inference, every routing decision can be
replayed and explained after the fact — which is exactly what the reason codes do.

## How I built it

**Data plane (Go).** Each request runs a fixed pipeline: admission control →
loop detection → deterministic scored routing → circuit breaker → generation →
semantic contract validation → quality and cost gates → escalation. Every stage
can veto, and every veto has a name.

**Control plane (NATS).** Node heartbeats, provider-health observations, chaos
commands, incidents, and escalations. Events carry IDs for idempotent delivery and
timestamps plus versions, so out-of-order arrivals are ignored rather than
corrupting state.

**Chaos engine.** Every failure mode is a single endpoint call that auto-restores.
No manual process kills — which means anyone can reproduce the entire demo.

**Dashboard (Next.js).** Polls aggregated node state and renders cluster health,
provider consensus, bulkhead saturation, and a live colour-coded reason-code
stream.

## Challenges I ran into

**Consensus without pretending to have Raft.** Three nodes independently observe a
provider and disagree. The obvious answer was Raft — and it was a trap. I could not
have implemented leader election and log replication correctly in a day, and
shipping a subtly broken consensus layer while claiming safety would have been
worse than not having one. I chose majority quorum and wrote the limitation into an
ADR: under partition, each node falls back to local observations and makes **no
global claim**. Honest and defensible beats impressive and unverifiable.

**Making recovery visible.** My first version detected failure in seconds but took
almost a minute to show recovery, because health was averaged over a fixed-size
probe buffer. Nobody watching a five-minute demo waits a minute. I bounded health
to a rolling 15-second window, so the system returns to green about as fast as it
went red.

**Consensus that survives node loss — visibly.** A dead node's last observations
were still counting toward quorum. Correct behaviour was to drop them the moment
the node is declared dead, which also made the demo honest: you watch
\\( \text{observations} : 3 \rightarrow 2 \\) and quorum hold.

## What I learned

This was my first serious Go codebase — I came at distributed systems from the AI
side, not the other way around. I went down the stack because the failures I cared
about could not be fixed at the application layer.

The bigger lesson was about **honesty as an engineering practice**. The strongest
parts of this project are the places where I wrote down what it *cannot* do:
ADR-006 states explicitly that this is not Raft, and the README says up front that
the provider layer is a deterministic simulator. Documenting the boundary made the
design better, because I stopped designing around a claim I couldn't support.

## What is real, and what is simulated

Stated plainly, because it matters:

- **Real:** the 3-node cluster, NATS gossip, majority health consensus,
  node-failure detection, deterministic routing, circuit breakers, admission
  control, semantic validation, agent-loop detection, budget governance,
  escalation, metrics, dashboard, and benchmarks. `go test -race` passes across the
  deterministic core.
- **Simulated:** the provider upstream is a deterministic simulator. It models the
  failure *modes* faithfully — 200-with-broken-contract, latency inflation, hard
  5xx — but is not a live OpenAI/Anthropic endpoint. The local fallback calls
  Ollama when configured, and otherwise returns a deterministic answer so the demo
  never depends on a GPU.
- **In-memory:** budgets, loop state, decision log, and incidents are node-local.
  Postgres and Redis are provisioned for the durable-audit and shared-state design
  but are not on the request path today.

Real provider connectors are one `Provider` interface implementation away. That was
deliberately out of scope: the reliability control plane is the project, not the
connectors.

## Measured results

Real numbers from the included load generator against the live 3-node cluster —
raw JSON is committed in `docs/benchmark-results/`.

| Load | Result |
|---|---|
| 1000 requests @ 50 concurrent, baseline | **100% success**, p95 200 ms |
| 100 requests during a **total provider outage** | **100% success**, p95 56 ms, all on the zero-cost fallback |

p95 stays flat from 1 to 50 concurrent, which means the entire pipeline —
admission, scoring, validation, breakers — adds no measurable tail latency.

## What's next

Real provider connectors, Redis-backed shared budget and loop state, a Postgres
audit log, task-specific response contracts, and Raft if formal safety under
partition ever becomes a requirement rather than a nice-to-have.

---

> **SignalMesh doesn't just keep AI running.**
> **It decides when AI is healthy enough to keep running without you.**
