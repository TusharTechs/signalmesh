package escalation

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"signalmesh/internal/cluster"
	"signalmesh/internal/events"
	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
	"signalmesh/internal/validator"
)

// Escalation is an explainable request for human attention.
type Escalation struct {
	EventID           string    `json:"event_id"`
	EscalationID      string    `json:"escalation_id"`
	NodeID            string    `json:"node_id"`
	RequestID         string    `json:"request_id"`
	AgentID           string    `json:"agent_id"`
	TaskType          string    `json:"task_type"`
	RiskLevel         string    `json:"risk_level"`
	Phase             string    `json:"phase"`
	Provider          string    `json:"provider,omitempty"`
	Confidence        float64   `json:"confidence"`
	Reason            string    `json:"reason"`
	ReasonCodes       []string  `json:"reason_codes"`
	RecommendedAction string    `json:"recommended_action"`
	CreatedAt         time.Time `json:"created_at"`
}

// FailureInfo describes the failure being evaluated.
type FailureInfo struct {
	Phase       string
	Provider    string
	Attempt     int
	Err         error
	Validation  validator.Result
	ReasonCodes []string
}

// Escalator decides when a failure requires human attention.
type Escalator struct {
	nodeID string
	bus    *events.Bus
	logger *slog.Logger

	mu    sync.RWMutex
	items []Escalation
}

// NewEscalator creates an escalation engine.
func NewEscalator(nodeID string, bus *events.Bus, logger *slog.Logger) *Escalator {
	return &Escalator{
		nodeID: nodeID,
		bus:    bus,
		logger: logger,
		items:  make([]Escalation, 0),
	}
}

func isCriticalTask(taskType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(taskType))

	critical := []string{
		"financial",
		"payment",
		"refund",
		"trade",
		"compliance",
		"medical",
		"security",
		"irreversible",
		"action",
	}

	for _, keyword := range critical {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}

	return false
}

// Consider evaluates whether the failure requires human attention.
func (e *Escalator) Consider(
	req providers.ModelRequest,
	pol policy.Policy,
	f FailureInfo,
) *Escalation {
	if f.Phase == "" {
		return nil
	}

	escalate := false
	reasons := make([]string, 0)

	add := func(code string) {
		reasons = append(reasons, code)
		escalate = true
	}

	confidence := 0.05

	if f.Validation.Valid {
		confidence = f.Validation.Score
	} else if f.Validation.Score > 0 {
		confidence = f.Validation.Score
	}

	if f.Phase == "no_provider" || f.Phase == "budget" {
		confidence = 0.0
	}

	if f.Phase == "agent_loop" {
		add("AGENT_LOOP_DETECTED")
	}

	if pol.RequiresHumanOnFailure {
		add("HIGH_RISK_POLICY_REQUIRES_HUMAN")
	}

	if pol.RiskLevel == policy.RiskHigh && !pol.Idempotent {
		if f.Phase == "timeout" || f.Phase == "generation" {
			add("IRREVERSIBLE_ACTION_UNCERTAIN_OUTCOME")
		}
	}

	if isCriticalTask(req.TaskType) && (f.Phase == "semantic" || f.Phase == "quality") {
		add("CRITICAL_TASK_CONTRACT_VIOLATION")
	}

	if f.Phase == "no_provider" && pol.RiskLevel == policy.RiskHigh {
		add("NO_PROVIDER_FOR_HIGH_RISK_TASK")
	}

	if f.Phase == "budget" && pol.RiskLevel == policy.RiskHigh {
		add("BUDGET_EXHAUSTED_HIGH_RISK_TASK")
	}

	if pol.RiskLevel == policy.RiskHigh && confidence < pol.MinimumQualityScore {
		if f.Phase == "semantic" || f.Phase == "quality" || f.Phase == "generation" || f.Phase == "timeout" {
			add("LOW_CONFIDENCE_HIGH_RISK")
		}
	}

	if !escalate {
		return nil
	}

	recommended := "HUMAN_REVIEW"
	if pol.RiskLevel == policy.RiskHigh && !pol.Idempotent {
		recommended = "HUMAN_REVIEW_BLOCK_AUTOMATIC_ACTION"
	}

	escalation := Escalation{
		EventID:           events.NewID("esc-event"),
		EscalationID:      events.NewID("esc"),
		NodeID:            e.nodeID,
		RequestID:         req.RequestID,
		AgentID:           req.AgentID,
		TaskType:          req.TaskType,
		RiskLevel:         string(pol.RiskLevel),
		Phase:             f.Phase,
		Provider:          f.Provider,
		Confidence:        confidence,
		Reason:            strings.Join(reasons, ", "),
		ReasonCodes:       reasons,
		RecommendedAction: recommended,
		CreatedAt:         time.Now(),
	}

	e.mu.Lock()
	e.items = append(e.items, escalation)
	e.mu.Unlock()

	if e.bus != nil {
		if err := e.bus.Publish(cluster.SubjectAttention, escalation); err != nil {
			e.logger.Warn("Failed to publish escalation", "error", err)
		}
	}

	e.logger.Warn(
		"Human attention required",
		"escalation_id", escalation.EscalationID,
		"request_id", req.RequestID,
		"agent_id", req.AgentID,
		"task_type", req.TaskType,
		"risk_level", string(pol.RiskLevel),
		"phase", f.Phase,
		"reasons", strings.Join(reasons, ","),
	)

	return &escalation
}

// List returns recorded escalations.
func (e *Escalator) List() []Escalation {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]Escalation, len(e.items))
	copy(out, e.items)

	return out
}
