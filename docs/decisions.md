# Decision register

Index of major architectural decisions. Full rationale lives in `docs/adr/`.

| ID | Decision | Summary |
|---|---|---|
| ADR-001 | Go data plane | Predictable concurrency, low overhead, race detector |
| ADR-002 | NATS over Kafka | Lightweight ops, sufficient for event-driven prototype |
| ADR-003 | PostgreSQL | Relational consistency for audit/policies |
| ADR-004 | Deterministic routing | "LLMs reason. Code guarantees." |
| ADR-005 | Semantic validation | HTTP 200 != AI success |
| ADR-006 | Majority quorum consensus | Simple and explainable; explicitly NOT Raft |
| ADR-007 | Local Ollama fallback | Emergency capacity, not the core innovation |
| ADR-008 | Not LiteLLM/Portkey | Different problem: distributed trust-aware reliability |

## Prototype-level decisions (not yet ADRs)

| Decision | Reason | Future work |
|---|---|---|
| Deterministic provider simulator instead of real OpenAI/Anthropic calls | Reproducible failure scenarios on stage; the reliability control plane is the project, not the connectors | Implement `Provider` for real APIs |
| `node-failure` = stop heartbeat (not `kill -9`) | One-endpoint, auto-restoring demo; identical cluster reaction | n/a — `kill -9` already works |
| Node-local budgets and loop state | Fast to build, easy to demo | Redis-backed shared state |
| Active health probes | Make degradation visible without user traffic | Passive observation in production |
| Static local fallback | Demo reliability without GPU/Ollama | Real Ollama deployment |
| In-memory decision log | Bounded, simple | PostgreSQL persistence |