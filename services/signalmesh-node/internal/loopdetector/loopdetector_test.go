package loopdetector

import (
	"testing"
	"time"

	"signalmesh/internal/providers"
)

func TestLoopDetection(t *testing.T) {
	d := New(2, time.Minute)

	req := providers.ModelRequest{
		AgentID:  "agent-a",
		TaskType: "financial_action",
		Model:    "mock-model",
		Messages: []providers.Message{
			{
				Role:    "user",
				Content: "approve payment",
			},
		},
	}

	fp := Fingerprint(req)

	detected, count := d.RecordFailure("agent-a", "financial_action", fp, "generation")
	if detected {
		t.Fatalf("expected no loop yet, count=%d", count)
	}

	detected, count = d.RecordFailure("agent-a", "financial_action", fp, "generation")
	if !detected {
		t.Fatalf("expected loop detection, count=%d", count)
	}

	if !d.IsLooping(fp) {
		t.Fatal("expected fingerprint to be looping")
	}

	d.RecordSuccess(fp)

	if d.IsLooping(fp) {
		t.Fatal("expected loop state to reset after success")
	}
}
