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

	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
	"signalmesh/internal/router"
	"signalmesh/internal/validator"
)

// Handler handles incoming AI-agent requests.
type Handler struct {
	router *router.Router
	logger *slog.Logger
}

// NewHandler creates a proxy handler.
func NewHandler(rtr *router.Router, logger *slog.Logger) *Handler {
	return &Handler{
		router: rtr,
		logger: logger,
	}
}

// HandleChat accepts chat completion requests.
func (h *Handler) HandleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", []string{"METHOD_NOT_ALLOWED"}, nil)
		return
	}

	var req providers.ModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request", []string{"INVALID_REQUEST_JSON"}, nil)
		return
	}

	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	pol := policy.FromRequest(req)

	deadline := time.Now().Add(pol.Deadline())
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()

	maxAttempts := pol.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	var lastDecision *router.Decision
	var lastValidation validator.Result

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		entry, decision, err := h.router.Select(req, pol)
		lastDecision = decision

		if err != nil {
			lastErr = err
			break
		}

		if !entry.Breaker.Allow() {
			lastErr = errors.New("circuit breaker rejected request")
			decision.ReasonCodes = append(decision.ReasonCodes, "CIRCUIT_BREAKER_REJECTED")

			if !h.canRetry(pol, attempt, lastErr, validator.Result{}, deadline) {
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
			"reason_codes", strings.Join(decision.ReasonCodes, ","),
		)

		resp, genErr := entry.Provider.Generate(ctx, req)
		if genErr != nil {
			entry.Breaker.RecordFailure()
			lastErr = genErr
			decision.ReasonCodes = append(decision.ReasonCodes, "PROVIDER_GENERATION_ERROR")

			h.logger.Warn(
				"Provider generation failed",
				"request_id", req.RequestID,
				"provider", entry.Provider.Name(),
				"attempt", attempt,
				"error", genErr,
			)

			if !h.canRetry(pol, attempt, genErr, validator.Result{}, deadline) {
				break
			}
			continue
		}

		contract := validator.DefaultContract()
		validation := validator.Validate(resp.Content, contract)
		lastValidation = validation

		if !validation.Valid {
			entry.Breaker.RecordFailure()
			lastErr = errors.New("semantic validation failed")
			decision.ReasonCodes = append(decision.ReasonCodes, "SEMANTIC_VALIDATION_FAILED")
			decision.ReasonCodes = append(decision.ReasonCodes, validation.ReasonStrings()...)

			h.logger.Warn(
				"Semantic validation failed",
				"request_id", req.RequestID,
				"provider", entry.Provider.Name(),
				"attempt", attempt,
				"details", validation.Details,
			)

			if !h.canRetry(pol, attempt, nil, validation, deadline) {
				break
			}
			continue
		}

		if validation.Score < pol.MinimumQualityScore {
			entry.Breaker.RecordFailure()
			lastErr = fmt.Errorf(
				"quality score %.2f below required %.2f",
				validation.Score,
				pol.MinimumQualityScore,
			)
			decision.ReasonCodes = append(decision.ReasonCodes, "QUALITY_BELOW_THRESHOLD")

			h.logger.Warn(
				"Quality below threshold",
				"request_id", req.RequestID,
				"provider", entry.Provider.Name(),
				"score", validation.Score,
				"required", pol.MinimumQualityScore,
			)

			if !h.canRetry(pol, attempt, nil, validation, deadline) {
				break
			}
			continue
		}

		if pol.MaxCostUSD > 0 && resp.EstimatedCost > pol.MaxCostUSD {
			entry.Breaker.RecordFailure()
			lastErr = fmt.Errorf(
				"estimated cost %.6f exceeds budget %.6f",
				resp.EstimatedCost,
				pol.MaxCostUSD,
			)
			decision.ReasonCodes = append(decision.ReasonCodes, "COST_BUDGET_EXCEEDED")

			h.logger.Warn(
				"Cost budget exceeded",
				"request_id", req.RequestID,
				"provider", entry.Provider.Name(),
				"estimated_cost", resp.EstimatedCost,
				"max_cost_usd", pol.MaxCostUSD,
			)

			if !h.canRetry(pol, attempt, lastErr, validation, deadline) {
				break
			}
			continue
		}

		entry.Breaker.RecordSuccess()
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

		h.writeSuccess(w, req.RequestID, resp, decision)
		return
	}

	h.writeFailure(w, req.RequestID, lastErr, lastDecision, lastValidation)
}

func (h *Handler) canRetry(
	pol policy.Policy,
	attempt int,
	err error,
	validation validator.Result,
	deadline time.Time,
) bool {
	if attempt >= pol.MaxRetries+1 {
		return false
	}

	// Do not retry if there is not enough latency budget left.
	if time.Now().After(deadline.Add(-100 * time.Millisecond)) {
		return false
	}

	// High-risk, non-idempotent operations must not be blindly retried.
	if pol.RiskLevel == policy.RiskHigh && !pol.Idempotent {
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline") {
				return false
			}
		}

		if !validation.Valid {
			return false
		}
	}

	return true
}

func (h *Handler) writeSuccess(
	w http.ResponseWriter,
	requestID string,
	resp *providers.ModelResponse,
	decision *router.Decision,
) {
	if decision != nil {
		w.Header().Set("X-SignalMesh-Provider", decision.SelectedProvider)
		w.Header().Set("X-SignalMesh-Fallback", fmt.Sprintf("%t", decision.FallbackUsed))
		w.Header().Set("X-SignalMesh-Reasons", strings.Join(decision.ReasonCodes, ","))
	}

	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) writeFailure(
	w http.ResponseWriter,
	requestID string,
	err error,
	decision *router.Decision,
	validation validator.Result,
) {
	status := http.StatusBadGateway
	reasonCodes := []string{}

	if decision != nil {
		reasonCodes = append(reasonCodes, decision.ReasonCodes...)
	}

	if len(validation.ReasonCodes) > 0 {
		reasonCodes = append(reasonCodes, validation.ReasonStrings()...)
	}

	message := "request failed"
	if err != nil {
		message = err.Error()

		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			reasonCodes = append(reasonCodes, "REQUEST_DEADLINE_EXCEEDED")
		}

		if strings.Contains(err.Error(), "no provider") {
			status = http.StatusServiceUnavailable
			reasonCodes = append(reasonCodes, "NO_PROVIDER_AVAILABLE")
		}

		if strings.Contains(err.Error(), "circuit breaker") {
			status = http.StatusServiceUnavailable
			reasonCodes = append(reasonCodes, "CIRCUIT_BREAKER_REJECTED")
		}
	}

	if len(reasonCodes) == 0 {
		reasonCodes = append(reasonCodes, "UNKNOWN_FAILURE")
	}

	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-SignalMesh-Reasons", strings.Join(reasonCodes, ","))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":        message,
		"reason_codes": reasonCodes,
		"decision":     decision,
	})
}

func writeError(
	w http.ResponseWriter,
	status int,
	message string,
	reasonCodes []string,
	decision *router.Decision,
) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-SignalMesh-Reasons", strings.Join(reasonCodes, ","))
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":        message,
		"reason_codes": reasonCodes,
		"decision":     decision,
	})
}
