package cluster

import "time"

const (
	SubjectNodeHeartbeat  = "signalmesh.health.node"
	SubjectProviderHealth = "signalmesh.health.provider"
	SubjectChaosProvider  = "signalmesh.chaos.provider"
)

// NodeHeartbeat is published by every SignalMesh node.
type NodeHeartbeat struct {
	EventID   string    `json:"event_id"`
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"`
	Version   uint64    `json:"version"`
}

// ProviderHealthObservation is a single node's observation of a provider.
type ProviderHealthObservation struct {
	EventID            string    `json:"event_id"`
	NodeID             string    `json:"node_id"`
	Provider           string    `json:"provider"`
	Timestamp          time.Time `json:"timestamp"`
	Version            uint64    `json:"version"`
	Status             string    `json:"status"`
	AvailabilityPct    float64   `json:"availability_pct"`
	P95LatencyMs       int64     `json:"p95_latency_ms"`
	ContractFailurePct float64   `json:"contract_failure_pct"`
}

// ChaosCommand is broadcast to all nodes to create deterministic demo failures.
type ChaosCommand struct {
	EventID      string    `json:"event_id"`
	SourceNodeID string    `json:"source_node_id"`
	Timestamp    time.Time `json:"timestamp"`
	Provider     string    `json:"provider"`
	LatencyMs    int       `json:"latency_ms"`
	ErrorRate    float64   `json:"error_rate"`
	ContractFail bool      `json:"contract_fail"`
}

// NodeStatus is the current liveness view of a node.
type NodeStatus struct {
	NodeID   string    `json:"node_id"`
	Alive    bool      `json:"alive"`
	LastSeen time.Time `json:"last_seen"`
}

// ProviderConsensusHealth is the cluster-level health view for a provider.
type ProviderConsensusHealth struct {
	Provider           string  `json:"provider"`
	Status             string  `json:"status"`
	AvailabilityPct    float64 `json:"availability_pct"`
	P95LatencyMs       int64   `json:"p95_latency_ms"`
	ContractFailurePct float64 `json:"contract_failure_pct"`
	Observations       int     `json:"observations"`
	Consensus          bool    `json:"consensus"`
}
