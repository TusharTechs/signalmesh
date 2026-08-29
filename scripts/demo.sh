#!/usr/bin/env bash
# SignalMesh five-minute demo driver.
#
# Every failure is triggered by ONE endpoint call and auto-restores. No
# terminal juggling, no manual process kills.
#
# Prerequisites:
#   make infra            # NATS
#   ./scripts/dev-cluster.sh   # 3 nodes + load balancer
#   make dashboard        # http://localhost:3000  (keep this on screen)

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NODE_A="${NODE_A:-http://localhost:8080}"
NODE_B="${NODE_B:-http://localhost:8081}"
LB="${LB:-http://localhost:9000}"
BENCH="$ROOT/services/signalmesh-node"

say() {
  echo
  echo "======================================================================"
  echo "SAY: $1"
  echo "======================================================================"
  read -r -p "[Enter to run this step] "
}

scenario() {
  local target="$1" name="$2" secs="${3:-45}"
  curl -s -X POST "$target/debug/chaos/scenario" \
    -H 'Content-Type: application/json' \
    -d "{\"scenario\":\"$name\",\"duration_seconds\":$secs}" \
    | jq -c '{scenario} + (if .note then {note} else {} end)'
}

send() {
  # send one request through the LB and print only the SignalMesh verdict headers
  curl -s -D - -o /dev/null -X POST "$LB/v1/chat/completions" \
    -H 'Content-Type: application/json' -d "$1" \
    | grep -iE '^HTTP/|x-signalmesh-(provider|phase|reasons|escalation)' || true
}

bench() { (cd "$BENCH" && go run ./cmd/signalmesh-bench "$@"); }

restore() { curl -s -X POST "$NODE_A/debug/chaos/scenario" -d '{"scenario":"restore"}' >/dev/null; }
trap restore EXIT

say "AI systems don't fail like normal web services. A dependency can return HTTP 200 and still be unusable, too slow, too expensive, or unsafe to trust. SignalMesh is the distributed reliability and attention control plane for AI agents."

say "STEP 1 - Normal operation. Three nodes, majority health consensus, healthy providers, low latency."
curl -s "$NODE_A/debug/cluster" | jq '{cluster_size, nodes:[.nodes[]|{node_id,alive}], providers:[.providers[]|{provider,status,observations,consensus}]}'
bench -url "$LB/v1/chat/completions" -n 20 -c 5 -task qa -risk low -agent demo-agent | jq '{successful,failed,p95_ms,providers}'

say "STEP 2 - Semantic degradation. The primary now returns HTTP 200 with a broken response contract. HTTP success is not AI success."
scenario "$NODE_A" semantic-degradation 60
echo "waiting for health observers..."; sleep 8
curl -s "$NODE_A/debug/provider-health" | jq -c '{"mock-primary":.["mock-primary"].status, contract_fail_pct:.["mock-primary"].contract_failure_pct}'
send '{"request_id":"demo-semantic","messages":[{"role":"user","content":"Capital of France?"}],"task_type":"qa","risk_level":"low","agent_id":"demo-agent"}'
restore

say "STEP 3 - Full provider outage. The circuit opens and traffic shifts to zero-cost emergency local capacity. Requests stay alive."
scenario "$NODE_A" provider-outage 60
sleep 8
send '{"request_id":"demo-outage","messages":[{"role":"user","content":"Capital of France?"}],"task_type":"qa","risk_level":"low","agent_id":"demo-agent"}'
curl -s "$NODE_A/debug/circuit" | jq -c
restore

say "STEP 4 - SignalMesh node failure. node-b stops heartbeating. The cluster ages it out, drops its observations from consensus, and keeps serving."
scenario "$NODE_B" node-failure 30
sleep 8
curl -s "$NODE_A/debug/cluster" | jq '{nodes:[.nodes[]|{node_id,alive}], providers:[.providers[]|{provider,observations,consensus}]}'
bench -url "$LB/v1/chat/completions" -n 20 -c 5 -task qa -risk low -agent demo-agent | jq '{successful,failed}'

say "STEP 5 - Agent retry loop. One agent hammers an identical failing high-risk call. SignalMesh detects the loop, trips the breaker, opens an incident, and escalates - runaway cost stopped."
scenario "$NODE_A" agent-loop 12
sleep 10
echo "Latest incident:"; curl -s "$NODE_A/debug/incidents" | jq -c '.[-1] | {type, severity, reason_codes, metadata}'
echo "Latest escalation:"; curl -s "$NODE_A/debug/escalations" | jq -c '.[-1] | {phase, reason_codes, recommended_action}'
restore

say "STEP 6 - Traffic spike. A burst of background load hits one node. Bulkhead isolation and bounded queues shed non-critical traffic while critical traffic is untouched."
scenario "$NODE_A" traffic-spike 15
sleep 8
curl -s "$NODE_A/debug/admission" | jq '{total_active, total_dropped, classes:(.classes|to_entries|map({(.key):{active:.value.active,dropped:.value.dropped}})|add)}'
restore
sleep 6

say "STEP 7 - Risk and economics. A tiny budget forces a zero-cost fallback for low-risk work. High-risk work with no safe provider escalates to a human instead of acting blindly. We route attention, not just requests."
curl -s -X POST "$NODE_A/debug/budget/set" -H 'Content-Type: application/json' -d '{"agent_id":"budget-agent","limit_usd":0.00005}' >/dev/null
echo "Low-risk, tiny budget:"
send '{"request_id":"demo-budget-low","messages":[{"role":"user","content":"Summarize ticket"}],"task_type":"summarization","risk_level":"low","agent_id":"budget-agent"}'
echo "High-risk, tiny budget:"
curl -s -X POST "$NODE_A/v1/chat/completions" -H 'Content-Type: application/json' \
  -d '{"request_id":"demo-budget-high","messages":[{"role":"user","content":"Approve refund?"}],"task_type":"financial_action","risk_level":"high","agent_id":"budget-agent"}' \
  | jq -c '{phase, reason_codes, recommended_action: .escalation.recommended_action}'

say "STEP 8 - Measured benchmarks. Real numbers, not invented."
echo "Pre-recorded (make benchmark):"
echo "  baseline  1000 req @ 50 concurrent : $(jq -c '{successful,failed,p95_ms}' "$ROOT/docs/benchmark-results/baseline-c50.json")"
echo "  outage     100 req @ 10 concurrent : $(jq -c '{successful,failed,p95_ms,providers}' "$ROOT/docs/benchmark-results/failover-outage.json")"
echo
restore
echo "letting the primary fully recover..."; sleep 15
echo "Live baseline now:"
bench -url "$LB/v1/chat/completions" -n 100 -c 10 -task qa -risk low -agent demo-agent \
  | jq '{successful,failed,p95_ms,providers}'

say "SignalMesh doesn't just keep AI running. It decides when AI is healthy enough to keep running without you."
echo
echo "Demo complete."
