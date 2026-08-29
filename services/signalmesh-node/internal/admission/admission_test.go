package admission

import (
	"context"
	"testing"

	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
)

func TestAdmissionLimit(t *testing.T) {
	m := NewManager(
		Config{Concurrency: 1, MaxQueue: 0},
		Config{Concurrency: 1, MaxQueue: 0},
		Config{Concurrency: 1, MaxQueue: 0},
		10,
	)

	ctx := context.Background()

	ok, _ := m.Acquire(ctx, ClassNormal)
	if !ok {
		t.Fatal("expected first request to be admitted")
	}

	ok, reasons := m.Acquire(ctx, ClassNormal)
	if ok {
		t.Fatal("expected second request to be rejected because queue is zero")
	}

	if len(reasons) == 0 {
		t.Fatal("expected rejection reasons")
	}

	m.Release(ClassNormal)

	ok, _ = m.Acquire(ctx, ClassNormal)
	if !ok {
		t.Fatal("expected request to be admitted after release")
	}
}

func TestClassifyHighRiskAsCritical(t *testing.T) {
	req := providers.ModelRequest{
		TaskType:  "financial_action",
		RiskLevel: "high",
	}

	pol := policy.FromRequest(req)

	class := Classify(req, pol)
	if class != ClassCritical {
		t.Fatalf("expected CRITICAL, got %s", class)
	}
}
