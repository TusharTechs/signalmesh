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

	"signalmesh/internal/budget"
	"signalmesh/internal/circuitbreaker"
	"signalmesh/internal/cluster"
	"signalmesh/internal/escalation"
	"signalmesh/internal/events"
	"signalmesh/internal/health"
	"signalmesh/internal/incident"
	"signalmesh/internal/loopdetector"
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

	handler := proxy.NewHandler(
		nodeID,
		rtr,
		logger,
		loopDetector,
		budgetManager,
		escalator,
		incidentReporter,
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

	srv := &http.Server{
		Addr:         ":" + httpPort,
		Handler:      mux,
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
