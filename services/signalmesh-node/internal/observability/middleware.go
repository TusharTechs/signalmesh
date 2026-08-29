package observability

import (
	"net/http"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.written {
		return
	}

	r.status = code
	r.written = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.written {
		r.status = http.StatusOK
		r.written = true
	}

	return r.ResponseWriter.Write(body)
}

// Middleware records metrics and decision logs for AI requests.
type Middleware struct {
	nodeID    string
	metrics   *Metrics
	decisions *DecisionLog
}

// NewMiddleware creates observability middleware.
func NewMiddleware(nodeID string, metrics *Metrics, decisions *DecisionLog) *Middleware {
	return &Middleware{
		nodeID:    nodeID,
		metrics:   metrics,
		decisions: decisions,
	}
}

// Wrap wraps an HTTP handler.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)

		requestID := rec.Header().Get("X-Request-ID")
		agentID := rec.Header().Get("X-SignalMesh-Agent")
		taskType := rec.Header().Get("X-SignalMesh-Task")
		riskLevel := rec.Header().Get("X-SignalMesh-Risk")
		trafficClass := rec.Header().Get("X-SignalMesh-Traffic-Class")
		provider := rec.Header().Get("X-SignalMesh-Provider")
		phase := rec.Header().Get("X-SignalMesh-Phase")
		fallback := rec.Header().Get("X-SignalMesh-Fallback") == "true"
		escalated := rec.Header().Get("X-SignalMesh-Escalation") == "true"

		reasons := splitCSV(rec.Header().Get("X-SignalMesh-Reasons"))

		success := rec.status >= 200 && rec.status < 300

		m.metrics.RecordRequest(duration, success, fallback)
		m.metrics.RecordProviderOutcome(provider, success)

		if contains(reasons, "SEMANTIC_VALIDATION_FAILED") {
			m.metrics.RecordSemanticFailure()
		}

		if escalated {
			m.metrics.RecordEscalation()
		}

		if contains(reasons, "ADMISSION_REJECTED") ||
			contains(reasons, "ADMISSION_QUEUE_FULL") ||
			contains(reasons, "GLOBAL_LOAD_SHEDDING") ||
			contains(reasons, "ADMISSION_TIMEOUT") {
			m.metrics.RecordAdmissionDropped()
		}

		if contains(reasons, "AGENT_LOOP_DETECTED") ||
			contains(reasons, "BUDGET_EXHAUSTED") {
			m.metrics.RecordIncident()
		}

		outcome := phase
		if outcome == "" {
			outcome = http.StatusText(rec.status)
		}

		m.decisions.Add(DecisionRecord{
			Timestamp:    time.Now(),
			RequestID:    requestID,
			AgentID:      agentID,
			TaskType:     taskType,
			RiskLevel:    riskLevel,
			TrafficClass: trafficClass,
			Provider:     provider,
			FallbackUsed: fallback,
			Status:       rec.status,
			Outcome:      outcome,
			ReasonCodes:  reasons,
			LatencyMs:    duration.Milliseconds(),
		})
	})
}

func splitCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}

	return out
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}

	return false
}
