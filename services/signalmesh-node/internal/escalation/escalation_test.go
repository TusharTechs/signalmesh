package escalation

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"signalmesh/internal/policy"
	"signalmesh/internal/providers"
)

func newEscalator() *Escalator {
	return NewEscalator("node-a", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func req(task, risk string) providers.ModelRequest {
	return providers.ModelRequest{RequestID: "r1", AgentID: "a1", TaskType: task, RiskLevel: risk}
}

func TestLowRiskGenerationFailureDoesNotEscalate(t *testing.T) {
	e := newEscalator()
	r := req("qa", "low")

	got := e.Consider(r, policy.FromRequest(r), FailureInfo{Phase: "generation", Err: errors.New("boom")})
	if got != nil {
		t.Fatalf("low-risk generation failure should not escalate, got %+v", got)
	}
}

func TestHighRiskNoProviderEscalatesAndBlocksAction(t *testing.T) {
	e := newEscalator()
	r := req("financial_action", "high")

	got := e.Consider(r, policy.FromRequest(r), FailureInfo{Phase: "no_provider"})
	if got == nil {
		t.Fatal("high-risk task with no provider must escalate")
	}
	if got.RecommendedAction != "HUMAN_REVIEW_BLOCK_AUTOMATIC_ACTION" {
		t.Fatalf("expected blocking recommendation, got %s", got.RecommendedAction)
	}
}

func TestAgentLoopAlwaysEscalates(t *testing.T) {
	e := newEscalator()
	r := req("qa", "low")

	got := e.Consider(r, policy.FromRequest(r), FailureInfo{Phase: "agent_loop"})
	if got == nil {
		t.Fatal("agent loop must escalate regardless of risk")
	}
}

func TestEmptyPhaseNeverEscalates(t *testing.T) {
	e := newEscalator()
	r := req("financial_action", "high")

	if got := e.Consider(r, policy.FromRequest(r), FailureInfo{}); got != nil {
		t.Fatalf("empty failure phase should not escalate, got %+v", got)
	}
}
