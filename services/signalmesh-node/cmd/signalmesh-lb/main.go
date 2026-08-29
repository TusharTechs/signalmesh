package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// LoadBalancer is a minimal round-robin load balancer for the hackathon demo.
// It retries the next backend on connection errors.
type LoadBalancer struct {
	backends []string
	current  atomic.Uint64
	client   *http.Client
	logger   *slog.Logger
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	listenPort := env("LB_PORT", "9000")
	backendsEnv := env(
		"LB_BACKENDS",
		"http://localhost:8080,http://localhost:8081,http://localhost:8082",
	)

	backends := make([]string, 0)

	for _, raw := range strings.Split(backendsEnv, ",") {
		backend := strings.TrimSpace(raw)
		if backend != "" {
			backends = append(backends, backend)
		}
	}

	if len(backends) == 0 {
		logger.Error("No backends configured")
		os.Exit(1)
	}

	lb := &LoadBalancer{
		backends: backends,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}

	mux := http.NewServeMux()
	mux.Handle("/", lb)

	srv := &http.Server{
		Addr:         ":" + listenPort,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	logger.Info(
		"SignalMesh LB listening",
		"port", listenPort,
		"backends", strings.Join(backends, ","),
	)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("LB server error", "error", err)
		os.Exit(1)
	}
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	attempts := len(lb.backends)
	var lastErr error

	for i := 0; i < attempts; i++ {
		idx := lb.next() % uint64(len(lb.backends))
		backend := lb.backends[idx]

		target := strings.TrimSuffix(backend, "/") + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}

		req, err := http.NewRequestWithContext(
			r.Context(),
			r.Method,
			target,
			bytes.NewReader(body),
		)
		if err != nil {
			lastErr = err
			continue
		}

		req.Header = r.Header.Clone()

		resp, err := lb.client.Do(req)
		if err != nil {
			lastErr = err
			lb.logger.Warn(
				"Backend request failed",
				"backend", backend,
				"error", err,
			)
			continue
		}

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		_ = resp.Body.Close()

		return
	}

	lb.logger.Error("All backends failed", "error", lastErr)
	http.Error(w, "all backends unavailable", http.StatusBadGateway)
}

func (lb *LoadBalancer) next() uint64 {
	return lb.current.Add(1)
}

func env(key string, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}
