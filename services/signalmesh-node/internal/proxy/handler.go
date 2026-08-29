package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"signalmesh/internal/admission"
	"signalmesh/internal/budget"
	"signalmesh/internal/escalation"
	"signalmesh/internal/incident"
	"signalmesh/internal/loopdetector"
	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
	"signalmesh/internal/router"
	"signalmesh/internal/validator"
)

type failureState struct {
	phase       string
	provider    string
	attempt     int
	err         error
	validation  validator.Result
	decision    *router.Decision
	reasonCodes []string
}

// Handler handles incoming AI-agent requests.
type Handler struct {
	nodeID       string
	router       *router.Router
	logger       *slog.Logger
	loopDetector *loopdetector.Detector
	budgets      *budget.Manager
	escalator    *escalation.Escalator
	incidents    *incident.Reporter
	admission    *admission.Manager
}

// NewHandler creates a proxy handler.
func NewHandler(
	nodeID string,
	rtr *router.Router,
	logger *slog.Logger,
	detector *loopdetector.Detector,
	budgets *budget.Manager,
	escalator *escalation.Escalator,
	incidents *incident.Reporter,
	admissionManager *admission.Manager,
) *Handler {
	return &Handler{
		nodeID:       nodeID,
		router:       rtr,
		logger:       logger,
		loopDetector: detector,
		budgets:      budgets,
		escalator:    escalator,
		incidents:    incidents,
		admission:    admissionManager,
	}
}

// HandleChat accepts chat completion requests.
func (h *Handler) HandleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(
			w,
			http.StatusMethodNotAllowed,
			"method not allowed",
			[]string{"METHOD_NOT_ALLOWED"},
		)
		return
	}

	var req providers.ModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid JSON request",
			[]string{"INVALID_REQUEST_JSON"},
		)
		return
	}

	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	w.Header().Set("X-SignalMesh-Agent", req.AgentID)
	w.Header().Set("X-SignalMesh-Task", req.TaskType)
	w.Header().Set("X-SignalMesh-Risk", req.RiskLevel)

	pol := policy.FromRequest(req)
	fingerprint := loopdetector.Fingerprint(req)
	trafficClass := admission.Classify(req, pol)

	w.Header().Set("X-SignalMesh-Traffic-Class", string(trafficClass))

	deadline := time.Now().Add(pol.Deadline())
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()

	acquired, admissionReasons := h.admission.Acquire(ctx, trafficClass)
	if !acquired {
		failure := failureState{
			phase: "admission",
			reasonCodes: append(
				[]string{"ADMISSION_REJECTED"},
				admissionReasons...,
			),
		}

		h.writeFailure(w, req.RequestID, failure, nil)
		return
	}
	defer h.admission.Release(trafficClass)

	// If this exact request pattern is already looping, reject it immediately.
	if h.loopDetector.IsLooping(fingerprint) {
		failure := failureState{
			phase: "agent_loop",
			reasonCodes: []string{
				"AGENT_LOOP_DETECTED",
				"RUNAWAY_RETRY_STOPPED",
			},
		}

		esc := h.escalator.Consider(req, pol, escalation.FailureInfo{
			Phase:       failure.phase,
			ReasonCodes: failure.reasonCodes,
		})

		if esc != nil {
			failure.reasonCodes = append(failure.reasonCodes, "HUMAN_ATTENTION_REQUIRED")
		}

		h.writeFailure(w, req.RequestID, failure, esc)
		return
	}

	maxAttempts := pol.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var last failureState

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		remainingBudget := h.budgets.Remaining(req.AgentID)

		entry, decision, err := h.router.Select(req, pol, remainingBudget)
		last.decision = decision

		if err != nil {
			last = failureState{
				phase:       "no_provider",
				attempt:     attempt,
				err:         err,
				decision:    decision,
				reasonCodes: append([]string{}, decision.ReasonCodes...),
			}

			if hasCostReason(decision) || remainingBudget <= 0 {
				last.phase = "budget"
				last.reasonCodes = append(last.reasonCodes, "BUDGET_EXHAUSTED")
			}

			detected := h.handleFailure(req, pol, fingerprint, nil, last, attempt)
			if detected {
				last.phase = "agent_loop"
				last.reasonCodes = append(last.reasonCodes, "AGENT_LOOP_DETECTED")
			}

			if detected || !h.canRetry(pol, attempt, deadline, last) {
				break
			}

			continue
		}

		if !entry.Breaker.Allow() {
			last = failureState{
				phase:    "circuit",
				provider: entry.Provider.Name(),
				attempt:  attempt,
				err:      errors.New("circuit breaker rejected request"),
				decision: decision,
				reasonCodes: append(
					decision.ReasonCodes,
					"CIRCUIT_BREAKER_REJECTED",
				),
			}

			detected := h.handleFailure(req, pol, fingerprint, entry, last, attempt)
			if detected {
				last.phase = "agent_loop"
				last.reasonCodes = append(last.reasonCodes, "AGENT_LOOP_DETECTED")
			}

			if detected || !h.canRetry(pol, attempt, deadline, last) {
				break
			}

			continue
		}

		h.logger.Info(
			"Routing request",
			"request_id", req.RequestID,
			"attempt", attempt,
			"provider", entry.Provider.Name(),
			"fallback", entry.IsFallback,
			"estimated_cost_usd", entry.CostUSD,
			"reason_codes", strings.Join(decision.ReasonCodes, ","),
		)

		resp, genErr := entry.Provider.Generate(ctx, req)
		if genErr != nil {
			entry.Breaker.RecordFailure()

			phase := "generation"
			if errors.Is(genErr, context.DeadlineExceeded) {
				phase = "timeout"
			}

			last = failureState{
				phase:    phase,
				provider: entry.Provider.Name(),
				attempt:  attempt,
				err:      genErr,
				decision: decision,
				reasonCodes: append(
					decision.ReasonCodes,
					"PROVIDER_GENERATION_ERROR",
				),
			}

			detected := h.handleFailure(req, pol, fingerprint, entry, last, attempt)
			if detected {
				last.phase = "agent_loop"
				last.reasonCodes = append(last.reasonCodes, "AGENT_LOOP_DETECTED")
			}

			if detected || !h.canRetry(pol, attempt, deadline, last) {
				break
			}

			continue
		}

		contract := validator.DefaultContract()
		validation := validator.Validate(resp.Content, contract)

		if !validation.Valid {
			entry.Breaker.RecordFailure()

			last = failureState{
				phase:      "semantic",
				provider:   entry.Provider.Name(),
				attempt:    attempt,
				err:        errors.New("semantic validation failed"),
				validation: validation,
				decision:   decision,
				reasonCodes: append(
					decision.ReasonCodes,
					append([]string{"SEMANTIC_VALIDATION_FAILED"}, validation.ReasonStrings()...)...,
				),
			}

			detected := h.handleFailure(req, pol, fingerprint, entry, last, attempt)
			if detected {
				last.phase = "agent_loop"
				last.reasonCodes = append(last.reasonCodes, "AGENT_LOOP_DETECTED")
			}

			if detected || !h.canRetry(pol, attempt, deadline, last) {
				break
			}

			continue
		}

		if validation.Score < pol.MinimumQualityScore {
			entry.Breaker.RecordFailure()

			last = failureState{
				phase:      "quality",
				provider:   entry.Provider.Name(),
				attempt:    attempt,
				err:        fmt.Errorf("quality score %.2f below required %.2f", validation.Score, pol.MinimumQualityScore),
				validation: validation,
				decision:   decision,
				reasonCodes: append(
					decision.ReasonCodes,
					"QUALITY_BELOW_THRESHOLD",
				),
			}

			detected := h.handleFailure(req, pol, fingerprint, entry, last, attempt)
			if detected {
				last.phase = "agent_loop"
				last.reasonCodes = append(last.reasonCodes, "AGENT_LOOP_DETECTED")
			}

			if detected || !h.canRetry(pol, attempt, deadline, last) {
				break
			}

			continue
		}

		if pol.MaxCostUSD > 0 && resp.EstimatedCost > pol.MaxCostUSD {
			entry.Breaker.RecordFailure()

			last = failureState{
				phase:      "cost",
				provider:   entry.Provider.Name(),
				attempt:    attempt,
				err:        fmt.Errorf("estimated cost %.6f exceeds request budget %.6f", resp.EstimatedCost, pol.MaxCostUSD),
				validation: validation,
				decision:   decision,
				reasonCodes: append(
					decision.ReasonCodes,
					"COST_BUDGET_EXCEEDED",
				),
			}

			detected := h.handleFailure(req, pol, fingerprint, entry, last, attempt)
			if detected {
				last.phase = "agent_loop"
				last.reasonCodes = append(last.reasonCodes, "AGENT_LOOP_DETECTED")
			}

			if detected || !h.canRetry(pol, attempt, deadline, last) {
				break
			}

			continue
		}

		entry.Breaker.RecordSuccess()
		h.loopDetector.RecordSuccess(fingerprint)
		h.budgets.Record(req.AgentID, resp.EstimatedCost)

		decision.ReasonCodes = append(decision.ReasonCodes, "POLICY_ACCEPTED")

		h.logger.Info(
			"Request accepted",
			"request_id", req.RequestID,
			"provider", entry.Provider.Name(),
			"attempt", attempt,
			"latency_ms", resp.LatencyMs,
			"quality_score", validation.Score,
			"estimated_cost", resp.EstimatedCost,
		)

		h.writeSuccess(w, req, resp, decision)
		return
	}

	if last.phase == "budget" {
		h.incidents.Report(
			"BUDGET_EXHAUSTED",
			req.RequestID,
			req.AgentID,
			last.provider,
			"HIGH",
			"Request budget or agent budget exhausted",
			last.reasonCodes,
			map[string]string{
				"task_type": req.TaskType,
			},
		)
	}

	esc := h.escalator.Consider(req, pol, escalation.FailureInfo{
		Phase:       last.phase,
		Provider:    last.provider,
		Attempt:     last.attempt,
		Err:         last.err,
		Validation:  last.validation,
		ReasonCodes: last.reasonCodes,
	})

	if esc != nil {
		last.reasonCodes = append(last.reasonCodes, "HUMAN_ATTENTION_REQUIRED")
	}

	h.writeFailure(w, req.RequestID, last, esc)
}

func (h *Handler) handleFailure(
	req providers.ModelRequest,
	pol policy.Policy,
	fingerprint string,
	entry *router.ProviderEntry,
	f failureState,
	attempt int,
) bool {
	reason := f.phase
	if reason == "" {
		reason = "unknown"
	}

	detected, count := h.loopDetector.RecordFailure(req.AgentID, req.TaskType, fingerprint, reason)
	if !detected {
		return false
	}

	if entry != nil {
		entry.Breaker.Trip()
	}

	h.incidents.Report(
		"AGENT_LOOP",
		req.RequestID,
		req.AgentID,
		f.provider,
		"CRITICAL",
		"Agent loop detected",
		[]string{"AGENT_LOOP_DETECTED"},
		map[string]string{
			"task_type":     req.TaskType,
			"failure_phase": f.phase,
			"count":         fmt.Sprintf("%d", count),
			"attempt":       fmt.Sprintf("%d", attempt),
		},
	)

	h.logger.Warn(
		"Agent loop detected",
		"request_id", req.RequestID,
		"agent_id", req.AgentID,
		"task_type", req.TaskType,
		"count", count,
	)

	return true
}

func (h *Handler) canRetry(
	pol policy.Policy,
	attempt int,
	deadline time.Time,
	f failureState,
) bool {
	if attempt >= pol.MaxRetries+1 {
		return false
	}

	if time.Now().After(deadline.Add(-100 * time.Millisecond)) {
		return false
	}

	switch f.phase {
	case "budget", "cost", "no_provider", "circuit", "agent_loop":
		return false
	}

	if pol.RiskLevel == policy.RiskHigh && !pol.Idempotent {
		if f.phase == "timeout" || f.phase == "generation" || f.phase == "semantic" {
			return false
		}
	}

	return true
}

func hasCostReason(decision *router.Decision) bool {
	if decision == nil {
		return false
	}

	for _, eval := range decision.Evaluations {
		for _, reason := range eval.ReasonCodes {
			if reason == "PROVIDER_COST_EXCEEDS_REMAINING_BUDGET" ||
				reason == "ESTIMATED_COST_EXCEEDS_REQUEST_BUDGET" ||
				reason == "REMAINING_BUDGET_NEGATIVE" {
				return true
			}
		}
	}

	return false
}

func (h *Handler) writeSuccess(
	w http.ResponseWriter,
	req providers.ModelRequest,
	resp *providers.ModelResponse,
	decision *router.Decision,
) {
	if decision != nil {
		w.Header().Set("X-SignalMesh-Phase", "accepted")
		w.Header().Set("X-SignalMesh-Provider", decision.SelectedProvider)
		w.Header().Set("X-SignalMesh-Fallback", fmt.Sprintf("%t", decision.FallbackUsed))
		w.Header().Set("X-SignalMesh-Reasons", strings.Join(decision.ReasonCodes, ","))
	}

	w.Header().Set("X-Request-ID", req.RequestID)
	w.Header().Set(
		"X-SignalMesh-Agent-Budget-Remaining",
		fmt.Sprintf("%.6f", h.budgets.Remaining(req.AgentID)),
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) writeFailure(
	w http.ResponseWriter,
	requestID string,
	f failureState,
	escalation *escalation.Escalation,
) {
	status := http.StatusBadGateway

	switch f.phase {
	case "timeout":
		status = http.StatusGatewayTimeout
	case "budget":
		status = http.StatusPaymentRequired
	case "agent_loop":
		status = http.StatusTooManyRequests
	case "semantic", "quality":
		status = http.StatusUnprocessableEntity
	case "no_provider", "circuit", "admission":
		status = http.StatusServiceUnavailable
	default:
		status = http.StatusBadGateway
	}

	message := "request failed"
	if f.err != nil {
		message = f.err.Error()
	} else if f.phase != "" {
		message = fmt.Sprintf("request failed during %s", f.phase)
	}

	if len(f.reasonCodes) == 0 {
		f.reasonCodes = append(f.reasonCodes, "UNKNOWN_FAILURE")
	}

	if f.phase == "" {
		w.Header().Set("X-SignalMesh-Phase", "unknown")
	} else {
		w.Header().Set("X-SignalMesh-Phase", f.phase)
	}

	if f.decision != nil {
		w.Header().Set("X-SignalMesh-Provider", f.decision.SelectedProvider)
		w.Header().Set("X-SignalMesh-Fallback", fmt.Sprintf("%t", f.decision.FallbackUsed))
	}

	if escalation != nil {
		w.Header().Set("X-SignalMesh-Escalation", "true")
		w.Header().Set("X-SignalMesh-Escalation-ID", escalation.EscalationID)
	}

	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-SignalMesh-Reasons", strings.Join(f.reasonCodes, ","))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":        message,
		"phase":        f.phase,
		"reason_codes": f.reasonCodes,
		"decision":     f.decision,
		"validation":   f.validation,
		"escalation":   escalation,
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string, reasonCodes []string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-SignalMesh-Reasons", strings.Join(reasonCodes, ","))
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":        message,
		"reason_codes": reasonCodes,
	})
}
