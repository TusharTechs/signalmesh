package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// LocalProvider is emergency local inference capacity.
//
// If OLLAMA_URL is configured and reachable, it calls Ollama.
// If Ollama is unavailable, it returns a deterministic static fallback so the
// demo remains reliable.
type LocalProvider struct {
	name   string
	url    string
	model  string
	client *http.Client
	logger *slog.Logger

	mu             sync.RWMutex
	totalReqs      int
	successReqs    int
	totalLatencyMs int64
}

// NewLocalProvider creates a local fallback provider.
func NewLocalProvider(name string, logger *slog.Logger) *LocalProvider {
	url := strings.TrimSpace(os.Getenv("OLLAMA_URL"))
	model := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))

	if model == "" {
		model = "llama3.2:1b"
	}

	return &LocalProvider{
		name:   name,
		url:    url,
		model:  model,
		logger: logger,
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

// Name returns the provider name.
func (p *LocalProvider) Name() string {
	return p.name
}

// Generate produces a local fallback response.
func (p *LocalProvider) Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error) {
	start := time.Now()

	// Health probes should be fast and deterministic.
	if req.TaskType == "probe" {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			p.record(false, time.Since(start))
			return nil, ctx.Err()
		}

		content := staticContent(req.TaskType, 0.72)
		p.record(true, time.Since(start))

		return p.buildResponse(req, content, time.Since(start)), nil
	}

	// Try Ollama if configured.
	if p.url != "" {
		answer, err := p.callOllama(ctx, req)
		if err == nil {
			content := wrapAnswer(answer, 0.75)
			p.record(true, time.Since(start))

			return p.buildResponse(req, content, time.Since(start)), nil
		}

		if ctx.Err() != nil {
			p.record(false, time.Since(start))
			return nil, ctx.Err()
		}

		p.logger.Warn(
			"Ollama fallback failed, using deterministic emergency response",
			"error", err,
		)
	}

	// Deterministic emergency fallback.
	select {
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		p.record(false, time.Since(start))
		return nil, ctx.Err()
	}

	content := staticContent(req.TaskType, 0.72)
	p.record(true, time.Since(start))

	return p.buildResponse(req, content, time.Since(start)), nil
}

// GetHealth returns local fallback health.
func (p *LocalProvider) GetHealth() ProviderHealth {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := "HEALTHY"
	availPct := 100.0

	if p.totalReqs > 0 {
		availPct = (float64(p.successReqs) / float64(p.totalReqs)) * 100.0
	}

	if availPct < 90.0 {
		status = "DEGRADED"
	}

	if availPct < 50.0 {
		status = "UNHEALTHY"
	}

	avgLatency := int64(0)
	if p.totalReqs > 0 {
		avgLatency = p.totalLatencyMs / int64(p.totalReqs)
	}

	return ProviderHealth{
		ProviderName:       p.name,
		Status:             status,
		AvailabilityPct:    availPct,
		P95LatencyMs:       avgLatency,
		ContractFailurePct: 0.0,
		LastUpdated:        time.Now(),
		Version:            1,
	}
}

func (p *LocalProvider) callOllama(ctx context.Context, req ModelRequest) (string, error) {
	messages := req.Messages
	if len(messages) == 0 {
		messages = []Message{
			{
				Role:    "user",
				Content: "Provide a short emergency fallback answer.",
			},
		}
	}

	payload := map[string]interface{}{
		"model":    p.model,
		"messages": messages,
		"stream":   false,
		"options": map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := strings.TrimSuffix(p.url, "/") + "/api/chat"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}

	answer := strings.TrimSpace(parsed.Message.Content)
	if answer == "" {
		answer = "Local fallback response"
	}

	return answer, nil
}

func (p *LocalProvider) buildResponse(req ModelRequest, content string, latency time.Duration) *ModelResponse {
	return &ModelResponse{
		ProviderName:  p.name,
		Model:         p.model,
		Content:       content,
		FinishReason:  "stop",
		InputTokens:   len(req.Messages) * 5,
		OutputTokens:  25,
		LatencyMs:     latency.Milliseconds(),
		EstimatedCost: 0.0,
	}
}

func (p *LocalProvider) record(success bool, latency time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.totalReqs++

	if success {
		p.successReqs++
	}

	p.totalLatencyMs += latency.Milliseconds()
}

func staticContent(taskType string, confidence float64) string {
	answer := fmt.Sprintf("Local emergency fallback for task %s", taskType)
	return wrapAnswer(answer, confidence)
}

func wrapAnswer(answer string, confidence float64) string {
	payload := map[string]interface{}{
		"answer":     answer,
		"confidence": confidence,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return `{"answer":"Local fallback response","confidence":0.72}`
	}

	return string(data)
}
