#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/docs/benchmark-results"
NODE_A="${NODE_A:-http://localhost:8080}"
URL="${URL:-http://localhost:9000/v1/chat/completions}"

mkdir -p "$OUT"

echo "==> Preflight checks"
curl -sf "$NODE_A/health" > /dev/null || {
  echo "ERROR: node-a not reachable. Start the cluster first: ./scripts/dev-cluster.sh"
  exit 1
}
curl -sf "http://localhost:9000/health" > /dev/null || {
  echo "ERROR: load balancer not reachable. Start the cluster first."
  exit 1
}

cd "$ROOT/services/signalmesh-node"

echo "==> Baseline benchmarks"
for c in 1 10 20 50; do
  n=$((c * 20))
  echo "--- concurrency=$c requests=$n ---"
  go run ./cmd/signalmesh-bench \
    -url "$URL" \
    -n "$n" \
    -c "$c" \
    -task qa \
    -risk low \
    -agent bench-agent | tee "$OUT/baseline-c$c.json"
done

echo
echo "==> Failover benchmark: provider outage"
curl -s -X POST "$NODE_A/debug/chaos/scenario" \
  -H 'Content-Type: application/json' \
  -d '{"scenario":"provider-outage","duration_seconds":45}' > /dev/null

sleep 6

go run ./cmd/signalmesh-bench \
  -url "$URL" \
  -n 100 \
  -c 10 \
  -task qa \
  -risk low \
  -agent bench-agent | tee "$OUT/failover-outage.json"

curl -s -X POST "$NODE_A/debug/chaos/scenario" \
  -H 'Content-Type: application/json' \
  -d '{"scenario":"restore"}' > /dev/null

echo
echo "Benchmark results saved to docs/benchmark-results/"