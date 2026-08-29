package cluster

import (
	"io"
	"log/slog"
	"testing"

	"signalmesh/internal/events"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSingleNodeObservation(t *testing.T) {
	store := NewStore("node-a", 1, (*events.Bus)(nil), testLogger())

	store.PublishProviderHealth(ProviderHealthObservation{
		Provider:        "mock-primary",
		Status:          "HEALTHY",
		AvailabilityPct: 100,
		P95LatencyMs:    100,
	})

	health := store.GetProviderHealth("mock-primary")

	if health.Status != "HEALTHY" {
		t.Fatalf("expected HEALTHY, got %s", health.Status)
	}
}
