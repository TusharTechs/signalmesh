package cluster

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"signalmesh/internal/events"
	"signalmesh/internal/providers"
)

const (
	observationTTL = 10 * time.Second
	nodeTTL        = 5 * time.Second
	seenTTL        = 60 * time.Second
)

// Store maintains distributed cluster state:
// - node heartbeats
// - provider health observations
// - majority consensus
// - idempotent event handling
type Store struct {
	mu          sync.RWMutex
	nodeID      string
	clusterSize int
	bus         *events.Bus
	logger      *slog.Logger

	nodes        map[string]NodeStatus
	observations map[string]map[string]ProviderHealthObservation
	local        map[string]providers.ProviderHealth
	seen         map[string]time.Time
	version      uint64

	chaosHandlers []func(ChaosCommand)
}

// NewStore creates a cluster state store.
func NewStore(nodeID string, clusterSize int, bus *events.Bus, logger *slog.Logger) *Store {
	if clusterSize <= 0 {
		clusterSize = 1
	}

	return &Store{
		nodeID:       nodeID,
		clusterSize:  clusterSize,
		bus:          bus,
		logger:       logger,
		nodes:        make(map[string]NodeStatus),
		observations: make(map[string]map[string]ProviderHealthObservation),
		local:        make(map[string]providers.ProviderHealth),
		seen:         make(map[string]time.Time),
	}
}

// OnChaos registers a handler for cluster-wide chaos commands.
func (s *Store) OnChaos(handler func(ChaosCommand)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.chaosHandlers = append(s.chaosHandlers, handler)
}

// Start subscribes to cluster subjects and launches background loops.
func (s *Store) Start(ctx context.Context) error {
	if s.bus != nil {
		if err := s.bus.Subscribe(SubjectNodeHeartbeat, s.handleHeartbeat); err != nil {
			return err
		}

		if err := s.bus.Subscribe(SubjectProviderHealth, s.handleProviderHealth); err != nil {
			return err
		}

		if err := s.bus.Subscribe(SubjectChaosProvider, s.handleChaos); err != nil {
			return err
		}
	}

	go s.heartbeatLoop(ctx)
	go s.cleanupLoop(ctx)

	return nil
}

func (s *Store) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Publish immediately on startup.
	s.publishHeartbeat()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.publishHeartbeat()
		}
	}
}

func (s *Store) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

func (s *Store) publishHeartbeat() {
	s.mu.Lock()
	s.version++
	hb := NodeHeartbeat{
		EventID:   events.NewID("hb"),
		NodeID:    s.nodeID,
		Timestamp: time.Now(),
		Status:    "HEALTHY",
		Version:   s.version,
	}
	s.mu.Unlock()

	s.markSeenLocal(hb.EventID)
	s.setNode(hb)

	if s.bus != nil {
		if err := s.bus.Publish(SubjectNodeHeartbeat, hb); err != nil {
			s.logger.Warn("Failed to publish heartbeat", "error", err)
		}
	}
}

func (s *Store) handleHeartbeat(data []byte) {
	var hb NodeHeartbeat
	if err := json.Unmarshal(data, &hb); err != nil {
		s.logger.Warn("Invalid heartbeat event", "error", err)
		return
	}

	if hb.EventID == "" || s.alreadySeen(hb.EventID) {
		return
	}

	s.setNode(hb)
}

func (s *Store) setNode(hb NodeHeartbeat) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.nodes[hb.NodeID]
	if ok && hb.Timestamp.Before(existing.LastSeen) {
		return
	}

	s.nodes[hb.NodeID] = NodeStatus{
		NodeID:   hb.NodeID,
		Alive:    true,
		LastSeen: hb.Timestamp,
	}
}

// SetLocalProviderHealth stores this node's local health observation.
func (s *Store) SetLocalProviderHealth(name string, health providers.ProviderHealth) {
	health.ProviderName = name

	if health.LastUpdated.IsZero() {
		health.LastUpdated = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.local[name] = health
}

// PublishProviderHealth records and publishes a local provider health observation.
func (s *Store) PublishProviderHealth(obs ProviderHealthObservation) {
	s.mu.Lock()
	s.version++
	obs.EventID = events.NewID("ph")
	obs.NodeID = s.nodeID
	obs.Timestamp = time.Now()
	obs.Version = s.version
	s.mu.Unlock()

	s.markSeenLocal(obs.EventID)
	s.setObservation(obs)

	if s.bus != nil {
		if err := s.bus.Publish(SubjectProviderHealth, obs); err != nil {
			s.logger.Warn(
				"Failed to publish provider health",
				"error", err,
				"provider", obs.Provider,
			)
		}
	}
}

func (s *Store) handleProviderHealth(data []byte) {
	var obs ProviderHealthObservation
	if err := json.Unmarshal(data, &obs); err != nil {
		s.logger.Warn("Invalid provider health event", "error", err)
		return
	}

	if obs.EventID == "" || s.alreadySeen(obs.EventID) {
		return
	}

	if time.Since(obs.Timestamp) > observationTTL {
		return
	}

	s.setObservation(obs)
}

func (s *Store) setObservation(obs ProviderHealthObservation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.observations[obs.Provider]; !ok {
		s.observations[obs.Provider] = make(map[string]ProviderHealthObservation)
	}

	existing, ok := s.observations[obs.Provider][obs.NodeID]
	if ok {
		// Ignore stale/out-of-order events.
		if obs.Timestamp.Before(existing.Timestamp) {
			return
		}

		if obs.Timestamp.Equal(existing.Timestamp) && obs.Version <= existing.Version {
			return
		}
	}

	s.observations[obs.Provider][obs.NodeID] = obs
}

// GetProviderHealth returns the consensus provider health if enough observations exist.
// Otherwise it falls back to the most recent observation or local health.
func (s *Store) GetProviderHealth(name string) providers.ProviderHealth {
	s.mu.RLock()
	local := s.local[name]
	now := time.Now()

	var recent []ProviderHealthObservation

	if byNode, ok := s.observations[name]; ok {
		for nodeID, obs := range byNode {
			if now.Sub(obs.Timestamp) > observationTTL {
				continue
			}

			if !s.nodeAliveLocked(nodeID, now) {
				continue
			}

			recent = append(recent, obs)
		}
	}

	clusterSize := s.clusterSize
	s.mu.RUnlock()

	if len(recent) == 0 {
		if local.ProviderName != "" {
			return local
		}

		return providers.ProviderHealth{
			ProviderName:    name,
			Status:          "UNKNOWN",
			AvailabilityPct: 100,
			LastUpdated:     now,
		}
	}

	counts := make(map[string]int)

	var availabilitySum float64
	var contractSum float64
	latencies := make([]int64, 0, len(recent))

	for _, obs := range recent {
		counts[obs.Status]++
		availabilitySum += obs.AvailabilityPct
		contractSum += obs.ContractFailurePct
		latencies = append(latencies, obs.P95LatencyMs)
	}

	required := clusterSize/2 + 1
	status := "UNKNOWN"

	for st, count := range counts {
		if count >= required {
			status = st
			break
		}
	}

	// If no majority is reached, use the latest observation.
	// This keeps small clusters and single-node mode usable.
	if status == "UNKNOWN" {
		latest := recent[0]
		for _, obs := range recent {
			if obs.Timestamp.After(latest.Timestamp) {
				latest = obs
			}
		}
		status = latest.Status
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p95 := latencies[len(latencies)-1]

	idx := int(float64(len(latencies)) * 0.95)
	if idx >= len(latencies) {
		idx = len(latencies) - 1
	}
	if idx >= 0 {
		p95 = latencies[idx]
	}

	return providers.ProviderHealth{
		ProviderName:       name,
		Status:             status,
		AvailabilityPct:    availabilitySum / float64(len(recent)),
		P95LatencyMs:       p95,
		ContractFailurePct: contractSum / float64(len(recent)),
		LastUpdated:        now,
		Version:            local.Version,
	}
}

func (s *Store) nodeAliveLocked(nodeID string, now time.Time) bool {
	if nodeID == s.nodeID {
		return true
	}

	node, ok := s.nodes[nodeID]
	if !ok {
		return false
	}

	return now.Sub(node.LastSeen) <= nodeTTL
}

// PublishChaos applies a chaos command locally and broadcasts it to the cluster.
func (s *Store) PublishChaos(cmd ChaosCommand) {
	if cmd.EventID == "" {
		cmd.EventID = events.NewID("chaos")
	}

	cmd.SourceNodeID = s.nodeID
	cmd.Timestamp = time.Now()

	s.markSeenLocal(cmd.EventID)

	s.mu.RLock()
	handlers := append([]func(ChaosCommand){}, s.chaosHandlers...)
	s.mu.RUnlock()

	for _, handler := range handlers {
		handler(cmd)
	}

	if s.bus != nil {
		if err := s.bus.Publish(SubjectChaosProvider, cmd); err != nil {
			s.logger.Warn("Failed to publish chaos command", "error", err)
		}
	}
}

func (s *Store) handleChaos(data []byte) {
	var cmd ChaosCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		s.logger.Warn("Invalid chaos command", "error", err)
		return
	}

	if cmd.EventID == "" || s.alreadySeen(cmd.EventID) {
		return
	}

	s.mu.RLock()
	handlers := append([]func(ChaosCommand){}, s.chaosHandlers...)
	s.mu.RUnlock()

	for _, handler := range handlers {
		handler(cmd)
	}
}

func (s *Store) markSeenLocal(eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seen[eventID] = time.Now()
}

func (s *Store) alreadySeen(eventID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[eventID]; ok {
		return true
	}

	s.seen[eventID] = time.Now()
	return false
}

func (s *Store) cleanup() {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, ts := range s.seen {
		if now.Sub(ts) > seenTTL {
			delete(s.seen, id)
		}
	}

	for provider, byNode := range s.observations {
		for nodeID, obs := range byNode {
			if now.Sub(obs.Timestamp) > observationTTL {
				delete(byNode, nodeID)
			}
		}

		if len(byNode) == 0 {
			delete(s.observations, provider)
		}
	}
}

// ClusterStatus is returned by the debug cluster endpoint.
type ClusterStatus struct {
	NodeID      string                    `json:"node_id"`
	ClusterSize int                       `json:"cluster_size"`
	Nodes       []NodeStatus              `json:"nodes"`
	Providers   []ProviderConsensusHealth `json:"providers"`
}

// Status returns a debuggable snapshot of cluster state.
func (s *Store) Status() ClusterStatus {
	now := time.Now()

	s.mu.RLock()

	nodes := make([]NodeStatus, 0, len(s.nodes))
	for _, node := range s.nodes {
		node.Alive = now.Sub(node.LastSeen) <= nodeTTL
		nodes = append(nodes, node)
	}

	providerNames := make(map[string]struct{})

	for name := range s.local {
		providerNames[name] = struct{}{}
	}

	for name := range s.observations {
		providerNames[name] = struct{}{}
	}

	s.mu.RUnlock()

	providersHealth := make([]ProviderConsensusHealth, 0, len(providerNames))

	for name := range providerNames {
		health := s.GetProviderHealth(name)

		observations := 0

		s.mu.RLock()
		if byNode, ok := s.observations[name]; ok {
			for _, obs := range byNode {
				if now.Sub(obs.Timestamp) <= observationTTL {
					observations++
				}
			}
		}
		s.mu.RUnlock()

		providersHealth = append(providersHealth, ProviderConsensusHealth{
			Provider:           name,
			Status:             health.Status,
			AvailabilityPct:    health.AvailabilityPct,
			P95LatencyMs:       health.P95LatencyMs,
			ContractFailurePct: health.ContractFailurePct,
			Observations:       observations,
			Consensus:          observations >= s.clusterSize/2+1,
		})
	}

	return ClusterStatus{
		NodeID:      s.nodeID,
		ClusterSize: s.clusterSize,
		Nodes:       nodes,
		Providers:   providersHealth,
	}
}
