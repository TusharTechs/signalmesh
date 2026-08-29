package observability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is a lightweight in-memory metrics registry.
// It exposes Prometheus-compatible metrics and dashboard-friendly snapshots.
type Metrics struct {
	nodeID    string
	startedAt time.Time

	requestsTotal         atomic.Int64
	successTotal          atomic.Int64
	failureTotal          atomic.Int64
	fallbackTotal         atomic.Int64
	semanticFailuresTotal atomic.Int64
	escalationsTotal      atomic.Int64
	incidentsTotal        atomic.Int64
	admissionDroppedTotal atomic.Int64

	mu               sync.Mutex
	latencies        []time.Duration
	providerRequests map[string]*atomic.Int64
	providerSuccess  map[string]*atomic.Int64
	providerFailures map[string]*atomic.Int64
}

// NewMetrics creates a metrics collector.
func NewMetrics(nodeID string) *Metrics {
	return &Metrics{
		nodeID:           nodeID,
		startedAt:        time.Now(),
		latencies:        make([]time.Duration, 0, 1024),
		providerRequests: make(map[string]*atomic.Int64),
		providerSuccess:  make(map[string]*atomic.Int64),
		providerFailures: make(map[string]*atomic.Int64),
	}
}

// RecordRequest records one completed AI request.
func (m *Metrics) RecordRequest(duration time.Duration, success bool, fallback bool) {
	m.requestsTotal.Add(1)

	if success {
		m.successTotal.Add(1)
	} else {
		m.failureTotal.Add(1)
	}

	if fallback {
		m.fallbackTotal.Add(1)
	}

	m.mu.Lock()
	m.latencies = append(m.latencies, duration)

	// Keep only the last 1000 latencies.
	if len(m.latencies) > 1000 {
		m.latencies = m.latencies[len(m.latencies)-1000:]
	}
	m.mu.Unlock()
}

// RecordProviderOutcome records provider-level request outcomes.
func (m *Metrics) RecordProviderOutcome(provider string, success bool) {
	if provider == "" {
		return
	}

	m.mu.Lock()

	reqCounter, ok := m.providerRequests[provider]
	if !ok {
		reqCounter = &atomic.Int64{}
		m.providerRequests[provider] = reqCounter
	}

	successCounter, ok := m.providerSuccess[provider]
	if !ok {
		successCounter = &atomic.Int64{}
		m.providerSuccess[provider] = successCounter
	}

	failureCounter, ok := m.providerFailures[provider]
	if !ok {
		failureCounter = &atomic.Int64{}
		m.providerFailures[provider] = failureCounter
	}

	m.mu.Unlock()

	reqCounter.Add(1)

	if success {
		successCounter.Add(1)
	} else {
		failureCounter.Add(1)
	}
}

func (m *Metrics) RecordSemanticFailure() {
	m.semanticFailuresTotal.Add(1)
}

func (m *Metrics) RecordEscalation() {
	m.escalationsTotal.Add(1)
}

func (m *Metrics) RecordIncident() {
	m.incidentsTotal.Add(1)
}

func (m *Metrics) RecordAdmissionDropped() {
	m.admissionDroppedTotal.Add(1)
}

// Percentiles returns p50, p95, and p99 latency in milliseconds.
func (m *Metrics) Percentiles() (int64, int64, int64) {
	m.mu.Lock()
	copyLatencies := make([]time.Duration, len(m.latencies))
	copy(copyLatencies, m.latencies)
	m.mu.Unlock()

	if len(copyLatencies) == 0 {
		return 0, 0, 0
	}

	sort.Slice(copyLatencies, func(i, j int) bool {
		return copyLatencies[i] < copyLatencies[j]
	})

	return percentile(copyLatencies, 50).Milliseconds(),
		percentile(copyLatencies, 95).Milliseconds(),
		percentile(copyLatencies, 99).Milliseconds()
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	idx := int((p / 100.0) * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	if idx < 0 {
		idx = 0
	}

	return sorted[idx]
}

// Snapshot returns dashboard-friendly metrics.
func (m *Metrics) Snapshot() map[string]interface{} {
	p50, p95, p99 := m.Percentiles()

	m.mu.Lock()
	providerOutcomes := make(map[string]interface{})

	for provider, counter := range m.providerRequests {
		providerOutcomes[provider] = map[string]int64{
			"requests": counter.Load(),
			"success":  m.providerSuccess[provider].Load(),
			"failures": m.providerFailures[provider].Load(),
		}
	}
	m.mu.Unlock()

	return map[string]interface{}{
		"node_id":                 m.nodeID,
		"uptime_seconds":          time.Since(m.startedAt).Seconds(),
		"requests_total":          m.requestsTotal.Load(),
		"success_total":           m.successTotal.Load(),
		"failure_total":           m.failureTotal.Load(),
		"fallback_total":          m.fallbackTotal.Load(),
		"semantic_failures_total": m.semanticFailuresTotal.Load(),
		"escalations_total":       m.escalationsTotal.Load(),
		"incidents_total":         m.incidentsTotal.Load(),
		"admission_dropped_total": m.admissionDroppedTotal.Load(),
		"p50_ms":                  p50,
		"p95_ms":                  p95,
		"p99_ms":                  p99,
		"provider_outcomes":       providerOutcomes,
	}
}

// Prometheus returns Prometheus text-format metrics.
func (m *Metrics) Prometheus() string {
	var b strings.Builder

	node := m.nodeID

	writeCounter := func(name string, help string, value int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n", name, help)
		fmt.Fprintf(&b, "# TYPE %s counter\n", name)
		fmt.Fprintf(&b, "%s{node_id=\"%s\"} %d\n\n", name, node, value)
	}

	writeCounter(
		"signalmesh_requests_total",
		"Total AI requests processed by this node.",
		m.requestsTotal.Load(),
	)

	writeCounter(
		"signalmesh_request_success_total",
		"Total successful AI requests.",
		m.successTotal.Load(),
	)

	writeCounter(
		"signalmesh_request_failure_total",
		"Total failed AI requests.",
		m.failureTotal.Load(),
	)

	writeCounter(
		"signalmesh_fallback_total",
		"Total requests served by fallback providers.",
		m.fallbackTotal.Load(),
	)

	writeCounter(
		"signalmesh_semantic_failure_total",
		"Total semantic validation failures.",
		m.semanticFailuresTotal.Load(),
	)

	writeCounter(
		"signalmesh_escalations_total",
		"Total human attention escalations.",
		m.escalationsTotal.Load(),
	)

	writeCounter(
		"signalmesh_incidents_total",
		"Total incidents reported.",
		m.incidentsTotal.Load(),
	)

	writeCounter(
		"signalmesh_admission_dropped_total",
		"Total requests dropped by admission control.",
		m.admissionDroppedTotal.Load(),
	)

	p50, p95, p99 := m.Percentiles()

	fmt.Fprintf(&b, "# HELP signalmesh_uptime_seconds Node uptime.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "signalmesh_uptime_seconds{node_id=\"%s\"} %f\n\n", node, time.Since(m.startedAt).Seconds())

	fmt.Fprintf(&b, "# HELP signalmesh_request_latency_p50_ms Request latency p50.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_request_latency_p50_ms gauge\n")
	fmt.Fprintf(&b, "signalmesh_request_latency_p50_ms{node_id=\"%s\"} %d\n\n", node, p50)

	fmt.Fprintf(&b, "# HELP signalmesh_request_latency_p95_ms Request latency p95.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_request_latency_p95_ms gauge\n")
	fmt.Fprintf(&b, "signalmesh_request_latency_p95_ms{node_id=\"%s\"} %d\n\n", node, p95)

	fmt.Fprintf(&b, "# HELP signalmesh_request_latency_p99_ms Request latency p99.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_request_latency_p99_ms gauge\n")
	fmt.Fprintf(&b, "signalmesh_request_latency_p99_ms{node_id=\"%s\"} %d\n\n", node, p99)

	m.mu.Lock()

	fmt.Fprintf(&b, "# HELP signalmesh_provider_requests_total Provider request count.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_provider_requests_total counter\n")
	for provider, counter := range m.providerRequests {
		fmt.Fprintf(
			&b,
			"signalmesh_provider_requests_total{node_id=\"%s\",provider=\"%s\"} %d\n",
			node,
			provider,
			counter.Load(),
		)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "# HELP signalmesh_provider_success_total Provider success count.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_provider_success_total counter\n")
	for provider, counter := range m.providerSuccess {
		fmt.Fprintf(
			&b,
			"signalmesh_provider_success_total{node_id=\"%s\",provider=\"%s\"} %d\n",
			node,
			provider,
			counter.Load(),
		)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "# HELP signalmesh_provider_failure_total Provider failure count.\n")
	fmt.Fprintf(&b, "# TYPE signalmesh_provider_failure_total counter\n")
	for provider, counter := range m.providerFailures {
		fmt.Fprintf(
			&b,
			"signalmesh_provider_failure_total{node_id=\"%s\",provider=\"%s\"} %d\n",
			node,
			provider,
			counter.Load(),
		)
	}
	b.WriteString("\n")

	m.mu.Unlock()

	return b.String()
}
