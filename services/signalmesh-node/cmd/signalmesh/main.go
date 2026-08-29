package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"signalmesh/internal/circuitbreaker"
	"signalmesh/internal/providers"
	"signalmesh/internal/proxy"
	"signalmesh/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting SignalMesh Node", "version", "0.2.0")

	mockProvider := providers.NewMockProvider("mock-primary", logger)
	primaryBreaker := circuitbreaker.New(circuitbreaker.DefaultConfig())

	rtr := router.New(logger)
	rtr.AddProvider(mockProvider, primaryBreaker, false)

	handler := proxy.NewHandler(rtr, logger)

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/chat/completions", handler.HandleChat)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Local development chaos endpoint.
	// This is intentionally simple for the hackathon prototype.
	// Do not expose this endpoint publicly without authentication.
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

		if cmd.LatencyMs <= 0 {
			cmd.LatencyMs = 150
		}

		mockProvider.InjectFailure(cmd.LatencyMs, cmd.ErrorRate, cmd.ContractFail)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "chaos injected",
		})
	})

	mux.HandleFunc("/debug/circuit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"mock-primary": string(primaryBreaker.State()),
		})
	})

	mux.HandleFunc("/debug/provider-health", func(w http.ResponseWriter, r *http.Request) {
		health := mockProvider.GetHealth()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(health)
	})

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("SignalMesh listening", "addr", srv.Addr)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down SignalMesh")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("SignalMesh stopped")
}
