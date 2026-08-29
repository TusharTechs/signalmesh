package chaos

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"signalmesh/internal/cluster"
)

// ScenarioRequest is the API body for running a chaos scenario.
type ScenarioRequest struct {
	Scenario        string `json:"scenario"`
	DurationSeconds int    `json:"duration_seconds"`
}

// ScenarioResult describes the active or completed scenario.
type ScenarioResult struct {
	RunID     int                  `json:"run_id"`
	Scenario  string               `json:"scenario"`
	StartedAt time.Time            `json:"started_at"`
	EndsAt    time.Time            `json:"ends_at,omitempty"`
	Command   cluster.ChaosCommand `json:"command"`
}

// Engine runs deterministic chaos scenarios across the cluster.
type Engine struct {
	store  *cluster.Store
	logger *slog.Logger

	mu     sync.Mutex
	runID  int
	active *ScenarioResult
	timer  *time.Timer
}

// NewEngine creates a chaos engine.
func NewEngine(store *cluster.Store, logger *slog.Logger) *Engine {
	return &Engine{
		store:  store,
		logger: logger,
	}
}

func normalCommand() cluster.ChaosCommand {
	return cluster.ChaosCommand{
		Provider:     "mock-primary",
		LatencyMs:    150,
		ErrorRate:    0.0,
		ContractFail: false,
	}
}

func commandForScenario(scenario string) (cluster.ChaosCommand, error) {
	switch scenario {
	case "provider-outage":
		return cluster.ChaosCommand{
			Provider:     "mock-primary",
			LatencyMs:    150,
			ErrorRate:    1.0,
			ContractFail: false,
		}, nil

	case "semantic-degradation":
		return cluster.ChaosCommand{
			Provider:     "mock-primary",
			LatencyMs:    150,
			ErrorRate:    0.0,
			ContractFail: true,
		}, nil

	case "latency-spike":
		return cluster.ChaosCommand{
			Provider:     "mock-primary",
			LatencyMs:    2000,
			ErrorRate:    0.0,
			ContractFail: false,
		}, nil

	default:
		return cluster.ChaosCommand{}, fmt.Errorf("unknown scenario: %s", scenario)
	}
}

// Run starts a scenario.
func (e *Engine) Run(req ScenarioRequest) (ScenarioResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}

	if req.Scenario == "restore" {
		cmd := normalCommand()
		e.store.PublishChaos(cmd)
		e.active = nil

		result := ScenarioResult{
			Scenario:  "restore",
			StartedAt: time.Now(),
			Command:   cmd,
		}

		e.logger.Info("Chaos restore command published")

		return result, nil
	}

	cmd, err := commandForScenario(req.Scenario)
	if err != nil {
		return ScenarioResult{}, err
	}

	duration := req.DurationSeconds
	if duration <= 0 {
		duration = 30
	}

	e.runID++
	id := e.runID

	e.store.PublishChaos(cmd)

	result := ScenarioResult{
		RunID:     id,
		Scenario:  req.Scenario,
		StartedAt: time.Now(),
		EndsAt:    time.Now().Add(time.Duration(duration) * time.Second),
		Command:   cmd,
	}

	e.active = &result

	e.timer = time.AfterFunc(time.Duration(duration)*time.Second, func() {
		e.autoRestore(id)
	})

	e.logger.Info(
		"Chaos scenario started",
		"scenario", req.Scenario,
		"duration_seconds", duration,
	)

	return result, nil
}

func (e *Engine) autoRestore(runID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.active == nil || e.active.RunID != runID {
		return
	}

	e.store.PublishChaos(normalCommand())

	e.logger.Info(
		"Chaos scenario auto-restored",
		"scenario", e.active.Scenario,
		"run_id", runID,
	)

	e.active = nil
	e.timer = nil
}

// Active returns the currently active scenario, if any.
func (e *Engine) Active() []ScenarioResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.active == nil {
		return []ScenarioResult{}
	}

	return []ScenarioResult{*e.active}
}
