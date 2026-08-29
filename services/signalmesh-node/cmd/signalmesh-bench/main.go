package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

type requestPayload struct {
	RequestID string    `json:"request_id"`
	Messages  []message `json:"messages"`
	Model     string    `json:"model"`
	TaskType  string    `json:"task_type"`
	RiskLevel string    `json:"risk_level"`
	AgentID   string    `json:"agent_id"`
	Priority  string    `json:"priority,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type result struct {
	Status   int
	Provider string
	Latency  time.Duration
	Err      error
}

func main() {
	url := flag.String("url", "http://localhost:9000/v1/chat/completions", "target URL")
	total := flag.Int("n", 100, "total number of requests")
	concurrency := flag.Int("c", 10, "number of concurrent workers")
	timeoutMs := flag.Int("timeout-ms", 10000, "HTTP client timeout in milliseconds")
	taskType := flag.String("task", "qa", "task type")
	risk := flag.String("risk", "low", "risk level")
	agent := flag.String("agent", "bench-agent", "agent id")
	priority := flag.String("priority", "", "traffic priority: critical, normal, background")
	model := flag.String("model", "mock-model", "model name")
	idPrefix := flag.String("id-prefix", "bench", "request id prefix")

	flag.Parse()

	if *total <= 0 {
		fmt.Println("total requests must be > 0")
		os.Exit(1)
	}

	if *concurrency <= 0 {
		*concurrency = 1
	}

	client := &http.Client{
		Timeout: time.Duration(*timeoutMs) * time.Millisecond,
	}

	jobs := make(chan int, *concurrency)
	results := make([]result, 0, *total)

	var mu sync.Mutex
	var wg sync.WaitGroup

	started := time.Now()

	for worker := 0; worker < *concurrency; worker++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for idx := range jobs {
				payload := requestPayload{
					RequestID: fmt.Sprintf("%s-%d", *idPrefix, idx),
					Messages: []message{
						{
							Role:    "user",
							Content: "Benchmark request.",
						},
					},
					Model:     *model,
					TaskType:  *taskType,
					RiskLevel: *risk,
					AgentID:   *agent,
					Priority:  *priority,
				}

				body, err := json.Marshal(payload)
				if err != nil {
					mu.Lock()
					results = append(results, result{Err: err})
					mu.Unlock()
					continue
				}

				req, err := http.NewRequest(http.MethodPost, *url, bytes.NewReader(body))
				if err != nil {
					mu.Lock()
					results = append(results, result{Err: err})
					mu.Unlock()
					continue
				}

				req.Header.Set("Content-Type", "application/json")

				start := time.Now()
				resp, err := client.Do(req)
				latency := time.Since(start)

				r := result{
					Latency: latency,
				}

				if err != nil {
					r.Err = err
				} else {
					r.Status = resp.StatusCode
					r.Provider = resp.Header.Get("X-SignalMesh-Provider")
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}

				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}()
	}

	for i := 0; i < *total; i++ {
		jobs <- i
	}

	close(jobs)
	wg.Wait()

	duration := time.Since(started)

	success := 0
	failed := 0
	statusCounts := make(map[int]int)
	providerCounts := make(map[string]int)
	latencies := make([]time.Duration, 0, len(results))

	for _, r := range results {
		if r.Err != nil {
			failed++
			statusCounts[0]++
			continue
		}

		statusCounts[r.Status]++

		if r.Status >= 200 && r.Status < 300 {
			success++
		} else {
			failed++
		}

		provider := r.Provider
		if provider == "" {
			provider = "unknown"
		}

		providerCounts[provider]++
		latencies = append(latencies, r.Latency)
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	summary := map[string]interface{}{
		"url":            *url,
		"total":          *total,
		"concurrency":    *concurrency,
		"started_at":     started,
		"duration_ms":    duration.Milliseconds(),
		"successful":     success,
		"failed":         failed,
		"requests_per_s": float64(*total) / duration.Seconds(),
		"status_codes":   statusCounts,
		"providers":      providerCounts,
		"p50_ms":         percentile(latencies, 50).Milliseconds(),
		"p95_ms":         percentile(latencies, 95).Milliseconds(),
		"p99_ms":         percentile(latencies, 99).Milliseconds(),
		"avg_ms":         average(latencies).Milliseconds(),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(summary)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	idx := int(math.Ceil(p/100.0*float64(len(sorted)))) - 1

	if idx < 0 {
		idx = 0
	}

	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return sorted[idx]
}

func average(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}

	var total time.Duration
	for _, v := range values {
		total += v
	}

	return total / time.Duration(len(values))
}
