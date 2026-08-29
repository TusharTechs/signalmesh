#!/usr/bin/env bash
# Usage: ./scripts/chaos.sh <scenario> [duration_seconds]
# Scenarios: provider-outage | semantic-degradation | latency-spike
#          | node-failure | traffic-spike | agent-loop | restore
# node-failure targets whichever node you point NODE_A at (default :8080).

NODE_A="${NODE_A:-http://localhost:8080}"
SCENARIO="${1:-restore}"
DURATION="${2:-30}"

curl -s -X POST "$NODE_A/debug/chaos/scenario" \
  -H "Content-Type: application/json" \
  -d "{\"scenario\":\"$SCENARIO\",\"duration_seconds\":$DURATION}" | jq