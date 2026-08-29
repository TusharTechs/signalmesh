#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> Starting infrastructure"
docker compose up -d nats postgres redis prometheus grafana

echo "==> Starting SignalMesh cluster + load balancer"
./scripts/dev-cluster.sh &
CLUSTER_PID=$!

echo "==> Starting dashboard on http://localhost:3000"
(cd apps/dashboard && npm run dev) &
DASH_PID=$!

trap 'kill $CLUSTER_PID $DASH_PID 2>/dev/null || true' INT TERM EXIT

echo
echo "Everything is running:"
echo "  Node A:  http://localhost:8080"
echo "  Node B:  http://localhost:8081"
echo "  Node C:  http://localhost:8082"
echo "  LB:      http://localhost:9000"
echo "  UI:      http://localhost:3000"
echo
echo "Press Ctrl+C to stop everything."

wait