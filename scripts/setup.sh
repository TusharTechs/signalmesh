#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> Checking prerequisites"
command -v go >/dev/null 2>&1 || { echo "ERROR: Go is not installed"; exit 1; }
command -v node >/dev/null 2>&1 || { echo "ERROR: Node is not installed"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "ERROR: Docker is not installed"; exit 1; }

echo "==> Pulling infrastructure images"
docker compose pull

echo "==> Building Go services"
cd services/signalmesh-node
go mod download
go build ./...
go vet ./...
cd ../..

echo "==> Installing dashboard dependencies"
cd apps/dashboard
npm install
cd ../..

echo
echo "Setup complete."
echo "Next steps:"
echo "  make infra      # start NATS/Postgres/Redis/Prometheus/Grafana"
echo "  make cluster    # start 3 nodes + load balancer"
echo "  make dashboard  # start the Next.js dashboard"