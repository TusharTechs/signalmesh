# Hostile judge Q&A prep

## Q1: Why isn't this just LiteLLM?

LiteLLM is a mature LLM gateway. SignalMesh is a distributed reliability
control plane: multi-node health consensus, semantic contract health,
agent-loop detection, and attention escalation. Different problem.

## Q2: What actually makes this distributed?

Multiple nodes, shared event bus, heartbeats, health observation
propagation, quorum consensus, node failure detection, and continued
service after node death.

## Q3: What happens when the network partitions?

Nodes fall back to local observations. No false global claims. This is a
documented trade-off of majority quorum, not Raft.

## Q4: What happens when events arrive out of order?

Observations carry timestamps and versions. Stale events are ignored.
Event IDs make delivery idempotent.

## Q5: What happens if a retry succeeds but the client timed out?

We treat timeout on non-idempotent high-risk work as unknown state and do
not blindly retry. Idempotency is policy-driven.

## Q6: Why use an LLM here?

We don't use one for control. LLMs are only optional semantic reasoners.
Code guarantees retries, budgets, routing, and coordination.

## Q7: How do you know the response is bad?

Explicit contracts: schema, required fields, confidence thresholds,
length limits. Deterministic validation, not hallucination guessing.

## Q8: How do you prevent runaway cost?

Request/agent/global budgets, loop detection, admission control, and
circuit breakers.

## Q9: How do you avoid false provider outage detection?

Multiple observations, thresholds, majority consensus, cooldowns, and
half-open recovery probes.

## Q10: What happens if SignalMesh itself fails?

Multiple nodes behind a load balancer. Dead nodes are detected by
heartbeat timeout and traffic continues.

## Q11: Are the providers real?

No — and we say so up front. `internal/providers/mock.go` is a deterministic
simulator so failure scenarios are reproducible on stage. It models the failure
*modes* that matter (HTTP 200 with a broken contract, latency inflation, hard
5xx). The `Provider` interface (`internal/providers/types.go`) is the only thing
a real OpenAI/Anthropic connector needs to implement; the reliability control
plane — routing, consensus, validation, escalation — is real code and is the
point of the project. The local fallback will call Ollama if `OLLAMA_URL` is set.

## Q12: How is "node failure" in the demo done?

The `node-failure` scenario stops that node's heartbeat; the process stays up but
the cluster ages it out and drops its observations from consensus. A hard
`kill -9` on the node process produces the identical cluster reaction — happy to
show that instead.

## Q13: Why are Postgres and Redis running if they're not used?

They're provisioned for the intended design (durable audit log in Postgres,
shared budget/loop state in Redis). In this one-day build all that state is
in-memory and node-local. Documented in `docs/decisions.md`, not hidden.

## Q14: Where's the test coverage?

`go test -race ./...` — unit tests on the deterministic core: router scoring and
failover, circuit breaker, admission/bulkheads, loop detection, semantic
validator, escalation policy, chaos engine, cluster store. The failure scenarios
themselves are the integration tests, driven live by the chaos engine.