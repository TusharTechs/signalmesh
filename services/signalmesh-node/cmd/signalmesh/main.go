package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"signalmesh/internal/admission"
	"signalmesh/internal/budget"
	"signalmesh/internal/chaos"
	"signalmesh/internal/circuitbreaker"
	"signalmesh/internal/cluster"
	"signalmesh/internal/escalation"
	"signalmesh/internal/events"
	"signalmesh/internal/health"
	"signalmesh/internal/incident"
	"signalmesh/internal/loopdetector"
	"signalmesh/internal/observability"
	"signalmesh/internal/providers"
	"signalmesh/internal/proxy"
	"signalmesh/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	nodeID := env("NODE_ID", "node-a")
	httpPort := env("HTTP_PORT", "8080")
	natsURL := env("NATS_URL", "nats://localhost:4222")
	clusterSize := envInt("SIGNALMESH_CLUSTER_SIZE", 3)

	globalBudget := envFloat("GLOBAL_BUDGET_USD", 100.0)
	defaultAgentBudget := envFloat("DEFAULT_AGENT_BUDGET_USD", 1.0)

	logger.Info(
		"Starting SignalMesh Node",
		"version", "0.4.0",
		"node_id", nodeID,
		"port", httpPort,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bus, err := events.Connect(natsURL, logger)
	if err != nil {
		logger.Warn(
			"NATS unavailable, running in standalone mode",
			"url", natsURL,
			"error", err,
		)
		bus = nil
	}
	defer bus.Close()

	store := cluster.NewStore(nodeID, clusterSize, bus, logger)

	mockProvider := providers.NewMockProvider("mock-primary", logger)
	localProvider := providers.NewLocalProvider("local-fallback", logger)

	store.SetLocalProviderHealth(mockProvider.Name(), mockProvider.GetHealth())
	store.SetLocalProviderHealth(localProvider.Name(), localProvider.GetHealth())

	store.OnChaos(func(cmd cluster.ChaosCommand) {
		if cmd.Provider == "" || cmd.Provider == mockProvider.Name() {
			latency := cmd.LatencyMs
			if latency <= 0 {
				latency = 150
			}

			mockProvider.InjectFailure(latency, cmd.ErrorRate, cmd.ContractFail)
		}
	})

	if err := store.Start(ctx); err != nil {
		logger.Error("Failed to start cluster store", "error", err)
		os.Exit(1)
	}

	primaryBreaker := circuitbreaker.New(circuitbreaker.DefaultConfig())
	localBreaker := circuitbreaker.New(circuitbreaker.DefaultConfig())

	rtr := router.New(logger, store)
	rtr.AddProviderWithCost(mockProvider, primaryBreaker, false, 0.0001)
	rtr.AddProviderWithCost(localProvider, localBreaker, true, 0.0)

	observer := health.NewObserver(
		nodeID,
		store,
		[]providers.Provider{mockProvider, localProvider},
		logger,
	)
	observer.Start(ctx)

	loopDetector := loopdetector.New(3, 60*time.Second)
	budgetManager := budget.NewManager(globalBudget, defaultAgentBudget)
	incidentReporter := incident.NewReporter(nodeID, bus, logger)
	escalator := escalation.NewEscalator(nodeID, bus, logger)
	selfURL := env("SELF_URL", "http://localhost:"+httpPort)
	chaosEngine := chaos.NewEngine(store, logger, selfURL)
	chaosEngine.OnRestore(func() {
		primaryBreaker.Reset()
		localBreaker.Reset()
	})

	metrics := observability.NewMetrics(nodeID)
	decisionLog := observability.NewDecisionLog(200)
	obsMiddleware := observability.NewMiddleware(nodeID, metrics, decisionLog)

	admissionManager := admission.NewManager(
		admission.Config{
			Concurrency: envInt("ADMISSION_CRITICAL_CONCURRENCY", 100),
			MaxQueue:    envInt("ADMISSION_CRITICAL_QUEUE", 100),
		},
		admission.Config{
			Concurrency: envInt("ADMISSION_NORMAL_CONCURRENCY", 50),
			MaxQueue:    envInt("ADMISSION_NORMAL_QUEUE", 50),
		},
		admission.Config{
			Concurrency: envInt("ADMISSION_BACKGROUND_CONCURRENCY", 10),
			MaxQueue:    envInt("ADMISSION_BACKGROUND_QUEUE", 10),
		},
		envInt("ADMISSION_GLOBAL_MAX_ACTIVE", 200),
	)

	handler := proxy.NewHandler(
		nodeID,
		rtr,
		logger,
		loopDetector,
		budgetManager,
		escalator,
		incidentReporter,
		admissionManager,
	)

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/chat/completions", handler.HandleChat)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/debug/chaos/inject", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var cmd struct {
			LatencyMs    int     `json:"latency_ms"`
			ErrorRate    float64 `json:"error_rate"`
			ContractFail bool    `json:"contract_fail"`
		}

		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		store.PublishChaos(cluster.ChaosCommand{
			Provider:     mockProvider.Name(),
			LatencyMs:    cmd.LatencyMs,
			ErrorRate:    cmd.ErrorRate,
			ContractFail: cmd.ContractFail,
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "chaos command published",
		})
	})

	mux.HandleFunc("/debug/circuit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			mockProvider.Name():  string(primaryBreaker.State()),
			localProvider.Name(): string(localBreaker.State()),
		})
	})

	mux.HandleFunc("/debug/provider-health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			mockProvider.Name():  store.GetProviderHealth(mockProvider.Name()),
			localProvider.Name(): store.GetProviderHealth(localProvider.Name()),
		})
	})

	mux.HandleFunc("/debug/cluster", func(w http.ResponseWriter, r *http.Request) {
		status := store.Status()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/debug/budget", func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agent_id")
		if agentID == "" {
			agentID = "demo-agent"
		}

		status := budgetManager.Status(agentID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/debug/budget/set", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var cmd struct {
			AgentID  string  `json:"agent_id"`
			LimitUSD float64 `json:"limit_usd"`
		}

		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		if cmd.AgentID == "" {
			http.Error(w, "agent_id is required", http.StatusBadRequest)
			return
		}

		budgetManager.SetAgentLimit(cmd.AgentID, cmd.LimitUSD)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "agent budget updated",
		})
	})

	mux.HandleFunc("/debug/incidents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(incidentReporter.List())
	})

	mux.HandleFunc("/debug/escalations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(escalator.List())
	})

	mux.HandleFunc("/debug/admission", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(admissionManager.Status())
	})

	mux.HandleFunc("/debug/chaos/scenario", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req chaos.ScenarioRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		result, err := chaosEngine.Run(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/debug/chaos/scenarios", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"available": chaosEngine.Scenarios(),
			"active":    chaosEngine.Active(),
		})
	})

	mux.HandleFunc("/metrics", cors(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(metrics.Prometheus()))
	}))

	mux.HandleFunc("/api/decisions", cors(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(decisionLog.Recent(100))
	}))

	mux.HandleFunc("/api/dashboard", cors(func(w http.ResponseWriter, r *http.Request) {
		clusterStatus := store.Status()

		providerHealth := map[string]interface{}{
			mockProvider.Name():  store.GetProviderHealth(mockProvider.Name()),
			localProvider.Name(): store.GetProviderHealth(localProvider.Name()),
		}

		circuits := map[string]string{
			mockProvider.Name():  string(primaryBreaker.State()),
			localProvider.Name(): string(localBreaker.State()),
		}

		payload := map[string]interface{}{
			"node": map[string]interface{}{
				"node_id": nodeID,
				"version": "0.6.0",
			},
			"cluster":            clusterStatus,
			"providers":          providerHealth,
			"circuits":           circuits,
			"admission":          admissionManager.Status(),
			"budget":             budgetManager.Status("demo-agent"),
			"chaos":              chaosEngine.Active(),
			"metrics":            metrics.Snapshot(),
			"recent_decisions":   decisionLog.Recent(20),
			"recent_escalations": lastEscalations(escalator.List(), 10),
			"recent_incidents":   lastIncidents(incidentReporter.List(), 10),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}))

	srv := &http.Server{
		Addr:         ":" + httpPort,
		Handler:      obsMiddleware.Wrap(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("SignalMesh listening", "addr", srv.Addr)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	logger.Info("Shutting down SignalMesh")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}

	logger.Info("SignalMesh stopped")
}

func env(key string, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}

	return i
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}

	return f
}

func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func lastEscalations(items []escalation.Escalation, n int) []escalation.Escalation {
	if n <= 0 || len(items) == 0 {
		return []escalation.Escalation{}
	}

	start := len(items) - n
	if start < 0 {
		start = 0
	}

	out := make([]escalation.Escalation, 0, len(items)-start)

	for i := len(items) - 1; i >= start; i-- {
		out = append(out, items[i])
	}

	return out
}

func lastIncidents(items []incident.Incident, n int) []incident.Incident {
	if n <= 0 || len(items) == 0 {
		return []incident.Incident{}
	}

	start := len(items) - n
	if start < 0 {
		start = 0
	}

	out := make([]incident.Incident, 0, len(items)-start)

	for i := len(items) - 1; i >= start; i-- {
		out = append(out, items[i])
	}

	return out
}
