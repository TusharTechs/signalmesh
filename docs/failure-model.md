# Failure model

| Failure | Detection | Response |
|---|---|---|
| Provider 5xx | error rate | retry if safe, circuit open |
| Timeout | deadline | no blind retry for high-risk |
| Rate limit | 429 | backoff/fallback |
| Latency spike | p95 vs budget | routing penalty/fallback |
| Semantic failure | contract validation | semantic failure, reroute |
| Low confidence | confidence threshold | quality failure/escalation |
| Cost spike | cost vs budget | cheaper provider/reject |
| Node failure | heartbeat timeout | traffic continues |
| Agent loop | repeated fingerprint failures | stop, trip breaker, escalate |
| Budget exhaustion | budget manager | fallback or reject + escalate |
| Duplicate events | event IDs | idempotent ignore |
| Out-of-order events | timestamps/versions | stale events ignored |