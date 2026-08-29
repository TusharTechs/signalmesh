package circuitbreaker

import (
	"testing"
	"time"
)

func TestBreakerOpensAndRecovers(t *testing.T) {
	cfg := Config{
		FailureThreshold:         2,
		Cooldown:                 50 * time.Millisecond,
		HalfOpenSuccessThreshold: 1,
		HalfOpenMaxProbes:        1,
	}

	b := New(cfg)

	if b.State() != StateClosed {
		t.Fatalf("expected initial state CLOSED, got %s", b.State())
	}

	b.RecordFailure()
	b.RecordFailure()

	if b.State() != StateOpen {
		t.Fatalf("expected state OPEN after failures, got %s", b.State())
	}

	if b.Allow() {
		t.Fatal("expected breaker to reject request while OPEN")
	}

	time.Sleep(60 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("expected breaker to allow half-open probe after cooldown")
	}

	b.RecordSuccess()

	if b.State() != StateClosed {
		t.Fatalf("expected state CLOSED after successful probe, got %s", b.State())
	}
}
