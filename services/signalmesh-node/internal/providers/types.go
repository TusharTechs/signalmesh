package providers

import (
	"context"
	"time"
)

// RequestPolicy allows clients to attach reliability, risk, and budget constraints
// to an AI request.
//
// Pointer fields are used so that absent fields can inherit defaults, while explicit
// false or zero values can override defaults.
type RequestPolicy struct {
	MaxLatencyMs           *int64   `json:"max_latency_ms,omitempty"`
	MaxCostUSD             *float64 `json:"max_cost_usd,omitempty"`
	MinimumQualityScore    *float64 `json:"minimum_quality_score,omitempty"`
	AllowLocalFallback     *bool    `json:"allow_local_fallback,omitempty"`
	RequiresHumanOnFailure *bool    `json:"requires_human_on_failure,omitempty"`
	MaxRetries             *int     `json:"max_retries,omitempty"`
	Idempotent             *bool    `json:"idempotent,omitempty"`
}

// ModelRequest is the standardized request SignalMesh sends to a provider.
type ModelRequest struct {
	RequestID      string          `json:"request_id"`
	Messages       []Message       `json:"messages"`
	Model          string          `json:"model"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Policy         *RequestPolicy  `json:"policy,omitempty"`

	// SignalMesh metadata
	TaskType  string `json:"task_type"`
	RiskLevel string `json:"risk_level"`
	AgentID   string `json:"agent_id"`
}

// Message is a chat-style message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat describes structured output requirements.
type ResponseFormat struct {
	Type   string      `json:"type"`
	Schema interface{} `json:"schema,omitempty"`
}

// ModelResponse is the standardized response from a provider.
type ModelResponse struct {
	ProviderName  string      `json:"provider_name"`
	Model         string      `json:"model"`
	Content       string      `json:"content"`
	FinishReason  string      `json:"finish_reason"`
	InputTokens   int         `json:"input_tokens"`
	OutputTokens  int         `json:"output_tokens"`
	LatencyMs     int64       `json:"latency_ms"`
	EstimatedCost float64     `json:"estimated_cost"`
	RawResponse   interface{} `json:"raw_response,omitempty"`
}

// ProviderHealth is a local observation of provider health.
// Later, this will be published to NATS and used for distributed consensus.
type ProviderHealth struct {
	ProviderName       string    `json:"provider_name"`
	Status             string    `json:"status"`
	AvailabilityPct    float64   `json:"availability_pct"`
	P95LatencyMs       int64     `json:"p95_latency_ms"`
	ContractFailurePct float64   `json:"contract_failure_pct"`
	LastUpdated        time.Time `json:"last_updated"`
	Version            uint64    `json:"version"`
}

// Provider is the interface all SignalMesh model providers must implement.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error)
	GetHealth() ProviderHealth
}
