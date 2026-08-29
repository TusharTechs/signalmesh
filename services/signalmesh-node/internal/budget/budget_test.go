package budget

import (
	"math"
	"testing"
)

func TestBudgetRemaining(t *testing.T) {
	m := NewManager(1.0, 0.1)

	remaining := m.Remaining("agent-a")
	if math.Abs(remaining-0.1) > 1e-9 {
		t.Fatalf("expected remaining 0.1, got %f", remaining)
	}

	m.Record("agent-a", 0.05)

	remaining = m.Remaining("agent-a")
	if math.Abs(remaining-0.05) > 1e-9 {
		t.Fatalf("expected remaining 0.05, got %f", remaining)
	}
}

func TestAgentLimitOverride(t *testing.T) {
	m := NewManager(10.0, 1.0)
	m.SetAgentLimit("agent-a", 0.01)

	remaining := m.Remaining("agent-a")
	if math.Abs(remaining-0.01) > 1e-9 {
		t.Fatalf("expected remaining 0.01, got %f", remaining)
	}
}
