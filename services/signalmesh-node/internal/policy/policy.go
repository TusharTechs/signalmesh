package policy

import (
	"strings"
	"time"

	"signalmesh/internal/providers"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "LOW"
	RiskMedium RiskLevel = "MEDIUM"
	RiskHigh   RiskLevel = "HIGH"
)

// Policy is the normalized, internal representation of request constraints.
type Policy struct {
	TaskType               string
	RiskLevel              RiskLevel
	MaxLatencyMs           int64
	MaxCostUSD             float64
	MinimumQualityScore    float64
	AllowLocalFallback     bool
	RequiresHumanOnFailure bool
	MaxRetries             int
	Idempotent             bool
}

func normalizeRisk(raw string) RiskLevel {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "high", "critical":
		return RiskHigh
	case "medium", "med":
		return RiskMedium
	default:
		return RiskLow
	}
}

func defaultPolicy(risk RiskLevel) Policy {
	switch risk {
	case RiskHigh:
		return Policy{
			RiskLevel:              RiskHigh,
			MaxLatencyMs:           1500,
			MaxCostUSD:             0.05,
			MinimumQualityScore:    0.85,
			AllowLocalFallback:     false,
			RequiresHumanOnFailure: true,
			MaxRetries:             1,
			Idempotent:             false,
		}
	case RiskMedium:
		return Policy{
			RiskLevel:              RiskMedium,
			MaxLatencyMs:           2000,
			MaxCostUSD:             0.02,
			MinimumQualityScore:    0.80,
			AllowLocalFallback:     true,
			RequiresHumanOnFailure: false,
			MaxRetries:             1,
			Idempotent:             true,
		}
	default:
		return Policy{
			RiskLevel:              RiskLow,
			MaxLatencyMs:           3000,
			MaxCostUSD:             0.01,
			MinimumQualityScore:    0.70,
			AllowLocalFallback:     true,
			RequiresHumanOnFailure: false,
			MaxRetries:             2,
			Idempotent:             true,
		}
	}
}

// FromRequest builds a normalized policy from the incoming request.
// If no explicit policy is provided, defaults are derived from risk level.
func FromRequest(req providers.ModelRequest) Policy {
	risk := normalizeRisk(req.RiskLevel)
	p := defaultPolicy(risk)
	p.TaskType = req.TaskType

	if req.Policy != nil {
		rp := req.Policy

		if rp.MaxLatencyMs != nil {
			p.MaxLatencyMs = *rp.MaxLatencyMs
		}

		if rp.MaxCostUSD != nil {
			p.MaxCostUSD = *rp.MaxCostUSD
		}

		if rp.MinimumQualityScore != nil {
			p.MinimumQualityScore = *rp.MinimumQualityScore
		}

		if rp.AllowLocalFallback != nil {
			p.AllowLocalFallback = *rp.AllowLocalFallback
		}

		if rp.RequiresHumanOnFailure != nil {
			p.RequiresHumanOnFailure = *rp.RequiresHumanOnFailure
		}

		if rp.MaxRetries != nil {
			p.MaxRetries = *rp.MaxRetries
		}

		if rp.Idempotent != nil {
			p.Idempotent = *rp.Idempotent
		}
	}

	if p.MaxLatencyMs <= 0 {
		p.MaxLatencyMs = 3000
	}

	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}

	if p.MinimumQualityScore < 0 {
		p.MinimumQualityScore = 0
	}

	return p
}

// Deadline returns the total request deadline as a duration.
func (p Policy) Deadline() time.Duration {
	if p.MaxLatencyMs <= 0 {
		return 3 * time.Second
	}

	return time.Duration(p.MaxLatencyMs) * time.Millisecond
}
