#!/usr/bin/env bash
# Starts 3 SignalMesh nodes + the load balancer in one terminal.
# Prerequisite: NATS running (make infra)

set -euo pipefail

cd "$(dirname "$0")/../services/signalmesh-node"

trap 'kill 0' EXIT

export NATS_URL="${NATS_URL:-nats://localhost:4222}"
export SIGNALMESH_CLUSTER_SIZE="${SIGNALMESH_CLUSTER_SIZE:-3}"

echo "Starting node-a (:8080), node-b (:8081), node-c (:8082), lb (:9000)..."
echo "Press Ctrl+C to stop all of them."

NODE_ID=node-a HTTP_PORT=8080 go run ./cmd/signalmesh &
NODE_ID=node-b HTTP_PORT=8081 go run ./cmd/signalmesh &
NODE_ID=node-c HTTP_PORT=8082 go run ./cmd/signalmesh &
LB_PORT=9000 go run ./cmd/signalmesh-lb &

wait
