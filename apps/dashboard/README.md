# SignalMesh dashboard

Next.js live dashboard for the SignalMesh control plane. Polls
`GET /api/dashboard` on a node every 2s and renders cluster health, provider
consensus, circuit state, admission / bulkhead counters, recent decisions,
incidents, and human-attention escalations.

## Run

```bash
npm install
npm run dev   # http://localhost:3000
```

Point it at a different node:

```bash
NEXT_PUBLIC_SIGNALMESH_NODE_URL=http://localhost:8081 npm run dev
```

Requires a running SignalMesh node (`./scripts/dev-cluster.sh` from the repo
root).
