package incident

import (
	"log/slog"
	"sync"
	"time"

	"signalmesh/internal/cluster"
	"signalmesh/internal/events"
)

// Incident is a machine-readable record of an AI-system incident.
type Incident struct {
	EventID     string            `json:"event_id"`
	IncidentID  string            `json:"incident_id"`
	Type        string            `json:"type"`
	NodeID      string            `json:"node_id"`
	RequestID   string            `json:"request_id"`
	AgentID     string            `json:"agent_id"`
	Provider    string            `json:"provider,omitempty"`
	Severity    string            `json:"severity"`
	Reason      string            `json:"reason"`
	ReasonCodes []string          `json:"reason_codes"`
	Timestamp   time.Time         `json:"timestamp"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Reporter records incidents locally and publishes them to NATS.
type Reporter struct {
	nodeID    string
	bus       *events.Bus
	logger    *slog.Logger
	mu        sync.RWMutex
	incidents []Incident
}

// NewReporter creates an incident reporter.
func NewReporter(nodeID string, bus *events.Bus, logger *slog.Logger) *Reporter {
	return &Reporter{
		nodeID:    nodeID,
		bus:       bus,
		logger:    logger,
		incidents: make([]Incident, 0),
	}
}

// Report creates, stores, and publishes an incident.
func (r *Reporter) Report(
	incidentType string,
	requestID string,
	agentID string,
	provider string,
	severity string,
	reason string,
	reasonCodes []string,
	metadata map[string]string,
) Incident {
	incident := Incident{
		EventID:     events.NewID("incident"),
		IncidentID:  events.NewID("inc"),
		Type:        incidentType,
		NodeID:      r.nodeID,
		RequestID:   requestID,
		AgentID:     agentID,
		Provider:    provider,
		Severity:    severity,
		Reason:      reason,
		ReasonCodes: reasonCodes,
		Timestamp:   time.Now(),
		Metadata:    metadata,
	}

	r.mu.Lock()
	r.incidents = append(r.incidents, incident)
	r.mu.Unlock()

	if r.bus != nil {
		if err := r.bus.Publish(cluster.SubjectIncident, incident); err != nil {
			r.logger.Warn("Failed to publish incident", "error", err)
		}
	}

	r.logger.Warn(
		"Incident reported",
		"incident_id", incident.IncidentID,
		"type", incident.Type,
		"agent_id", agentID,
		"request_id", requestID,
		"severity", severity,
	)

	return incident
}

// List returns a copy of recorded incidents.
func (r *Reporter) List() []Incident {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Incident, len(r.incidents))
	copy(out, r.incidents)

	return out
}
