package chaos

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"signalmesh/internal/cluster"
)

func newTestEngine() (*Engine, *cluster.Store) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	store := cluster.NewStore("node-test", 3, nil, logger)
	return NewEngine(store, logger, "http://localhost:0"), store
}

func TestUnknownScenarioRejected(t *testing.T) {
	e, _ := newTestEngine()
	if _, err := e.Run(ScenarioRequest{Scenario: "not-a-real-scenario"}); err == nil {
		t.Fatal("expected error for unknown scenario")
	}
}

func TestNodeFailureTogglesHeartbeat(t *testing.T) {
	e, store := newTestEngine()

	if !store.HeartbeatEnabled() {
		t.Fatal("heartbeat should start enabled")
	}

	if _, err := e.Run(ScenarioRequest{Scenario: "node-failure", DurationSeconds: 1}); err != nil {
		t.Fatalf("node-failure run failed: %v", err)
	}
	if store.HeartbeatEnabled() {
		t.Fatal("node-failure should disable heartbeat")
	}

	// Auto-restore fires after the duration.
	time.Sleep(1500 * time.Millisecond)
	if !store.HeartbeatEnabled() {
		t.Fatal("auto-restore should re-enable heartbeat")
	}
}

func TestRestoreReenablesHeartbeat(t *testing.T) {
	e, store := newTestEngine()

	_, _ = e.Run(ScenarioRequest{Scenario: "node-failure", DurationSeconds: 30})
	if _, err := e.Run(ScenarioRequest{Scenario: "restore"}); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if !store.HeartbeatEnabled() {
		t.Fatal("restore should re-enable heartbeat immediately")
	}
	if len(e.Active()) != 0 {
		t.Fatal("restore should clear the active scenario")
	}
}
