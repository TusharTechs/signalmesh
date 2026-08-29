package chaos

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
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
	Note      string               `json:"note,omitempty"`
}

// Engine runs deterministic chaos scenarios across the cluster.
//
// Every scenario is triggered by a single POST to /debug/chaos/scenario and
// auto-restores after its duration, so the whole demo is "hit one endpoint,
// watch the dashboard react" with no terminal juggling.
type Engine struct {
	store   *cluster.Store
	logger  *slog.Logger
	selfURL string
	client  *http.Client

	mu          sync.Mutex
	runID       int
	active      *ScenarioResult
	timer       *time.Timer
	loadStop    context.CancelFunc
	restoreHook func()
}

// OnRestore registers a callback run whenever the cluster is restored to normal
// (explicit "restore" or a scenario auto-restoring). Used to reset circuit
// breakers so the system returns to a pristine state after a demo scenario.
func (e *Engine) OnRestore(fn func()) {
	e.mu.Lock()
	e.restoreHook = fn
	e.mu.Unlock()
}

func (e *Engine) runRestoreHookLocked() {
	if e.restoreHook != nil {
		e.restoreHook()
	}
}

// NewEngine creates a chaos engine. selfURL is this node's own base URL, used
// by load-driven scenarios (e.g. traffic-spike, agent-loop).
func NewEngine(store *cluster.Store, logger *slog.Logger, selfURL string) *Engine {
	return &Engine{
		store:   store,
		logger:  logger,
		selfURL: selfURL,
		client:  &http.Client{Timeout: 5 * time.Second},
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

// providerScenarios are scenarios expressed purely as a provider chaos command.
func commandForScenario(scenario string) (cluster.ChaosCommand, bool) {
	switch scenario {
	case "provider-outage":
		return cluster.ChaosCommand{Provider: "mock-primary", LatencyMs: 150, ErrorRate: 1.0}, true
	case "semantic-degradation":
		return cluster.ChaosCommand{Provider: "mock-primary", LatencyMs: 150, ContractFail: true}, true
	case "latency-spike":
		return cluster.ChaosCommand{Provider: "mock-primary", LatencyMs: 2000}, true
	default:
		return cluster.ChaosCommand{}, false
	}
}

var knownScenarios = []string{
	"provider-outage",
	"semantic-degradation",
	"latency-spike",
	"node-failure",
	"traffic-spike",
	"agent-loop",
	"restore",
}

// Run starts a scenario.
func (e *Engine) Run(req ScenarioRequest) (ScenarioResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.stopLocked()

	if req.Scenario == "restore" {
		cmd := normalCommand()
		e.store.PublishChaos(cmd)
		e.store.SetHeartbeatEnabled(true)
		e.runRestoreHookLocked()
		e.active = nil

		e.logger.Info("Chaos restore command published")

		return ScenarioResult{Scenario: "restore", StartedAt: time.Now(), Command: cmd}, nil
	}

	duration := req.DurationSeconds
	if duration <= 0 {
		duration = 30
	}

	e.runID++
	id := e.runID
	now := time.Now()

	result := ScenarioResult{
		RunID:     id,
		Scenario:  req.Scenario,
		StartedAt: now,
		EndsAt:    now.Add(time.Duration(duration) * time.Second),
		Command:   normalCommand(),
	}

	switch {
	case isProviderScenario(req.Scenario):
		cmd, _ := commandForScenario(req.Scenario)
		e.store.PublishChaos(cmd)
		result.Command = cmd

	case req.Scenario == "node-failure":
		e.store.SetHeartbeatEnabled(false)
		result.Note = "this node stopped heartbeating; the cluster ages it out and keeps serving"

	case req.Scenario == "traffic-spike":
		e.startLoadLocked(loadSpec{workers: 120, taskType: "batch-index", risk: "low", priority: "background", agent: "spike-agent"}, duration)
		result.Note = "background load burst; watch admission_dropped_total and bulkhead isolation"

	case req.Scenario == "agent-loop":
		e.startLoadLocked(loadSpec{workers: 1, taskType: "financial_action", risk: "high", priority: "critical", agent: "loop-agent", fixedPrompt: "Approve the $5,000 refund now.", serial: true}, duration)
		e.store.PublishChaos(cluster.ChaosCommand{Provider: "mock-primary", LatencyMs: 150, ErrorRate: 1.0})
		result.Command = cluster.ChaosCommand{Provider: "mock-primary", LatencyMs: 150, ErrorRate: 1.0}
		result.Note = "one agent retries an identical failing high-risk call; loop detection trips the breaker and escalates"

	default:
		e.runID--
		return ScenarioResult{}, fmt.Errorf("unknown scenario: %s (known: %v)", req.Scenario, knownScenarios)
	}

	e.active = &result
	e.timer = time.AfterFunc(time.Duration(duration)*time.Second, func() { e.autoRestore(id) })

	e.logger.Info("Chaos scenario started", "scenario", req.Scenario, "duration_seconds", duration)

	return result, nil
}

func isProviderScenario(scenario string) bool {
	_, ok := commandForScenario(scenario)
	return ok
}

func (e *Engine) autoRestore(runID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.active == nil || e.active.RunID != runID {
		return
	}

	scenario := e.active.Scenario
	e.stopLocked()
	e.store.PublishChaos(normalCommand())
	e.store.SetHeartbeatEnabled(true)
	e.runRestoreHookLocked()
	e.active = nil

	e.logger.Info("Chaos scenario auto-restored", "scenario", scenario, "run_id", runID)
}

// stopLocked cancels any running timer and load generator. Caller holds e.mu.
func (e *Engine) stopLocked() {
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}

	if e.loadStop != nil {
		e.loadStop()
		e.loadStop = nil
	}
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

// Scenarios lists the scenario names this engine understands.
func (e *Engine) Scenarios() []string {
	return knownScenarios
}

type loadSpec struct {
	workers     int
	taskType    string
	risk        string
	priority    string
	agent       string
	fixedPrompt string
	serial      bool
}

// startLoadLocked spawns a bounded burst of internal requests against this node
// for the scenario duration. Caller holds e.mu.
func (e *Engine) startLoadLocked(spec loadSpec, durationSeconds int) {
	ctx, cancel := context.WithCancel(context.Background())
	e.loadStop = cancel

	deadline := time.Now().Add(time.Duration(durationSeconds) * time.Second)

	for i := 0; i < spec.workers; i++ {
		go func() {
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return
				default:
				}

				e.fireOne(ctx, spec)

				pause := 25 * time.Millisecond
				if spec.serial {
					pause = 400 * time.Millisecond
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(pause):
				}
			}
		}()
	}
}

func (e *Engine) fireOne(ctx context.Context, spec loadSpec) {
	prompt := spec.fixedPrompt
	if prompt == "" {
		prompt = "chaos load"
	}

	body := fmt.Sprintf(
		`{"messages":[{"role":"user","content":%q}],"model":"mock-model","task_type":%q,"risk_level":%q,"priority":%q,"agent_id":%q}`,
		prompt, spec.taskType, spec.risk, spec.priority, spec.agent,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.selfURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return
	}

	_ = resp.Body.Close()
}
