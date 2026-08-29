package loopdetector

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"signalmesh/internal/providers"
)

type entry struct {
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
	Reasons   []string
	Notified  bool
}

// Detector identifies repeated failure patterns from the same agent.
type Detector struct {
	mu        sync.Mutex
	threshold int
	window    time.Duration
	entries   map[string]*entry
}

// New creates an agent-loop detector.
func New(threshold int, window time.Duration) *Detector {
	if threshold <= 0 {
		threshold = 3
	}

	if window <= 0 {
		window = 60 * time.Second
	}

	return &Detector{
		threshold: threshold,
		window:    window,
		entries:   make(map[string]*entry),
	}
}

// Fingerprint creates a stable hash of the agent request pattern.
// It intentionally excludes request IDs so repeated retries can be detected.
func Fingerprint(req providers.ModelRequest) string {
	agent := strings.TrimSpace(req.AgentID)
	if agent == "" {
		agent = "anonymous"
	}

	h := sha256.New()

	h.Write([]byte(agent))
	h.Write([]byte("|"))
	h.Write([]byte(req.TaskType))
	h.Write([]byte("|"))
	h.Write([]byte(req.Model))

	for _, msg := range req.Messages {
		h.Write([]byte("|"))
		h.Write([]byte(msg.Role))
		h.Write([]byte(":"))
		h.Write([]byte(msg.Content))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// RecordFailure records a failure for a fingerprint.
// It returns true only the first time the loop threshold is crossed.
func (d *Detector) RecordFailure(agentID string, taskType string, fingerprint string, reason string) (bool, int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	e, ok := d.entries[fingerprint]
	if !ok || now.Sub(e.LastSeen) > d.window {
		e = &entry{
			FirstSeen: now,
			LastSeen:  now,
		}
		d.entries[fingerprint] = e
	}

	e.Count++
	e.LastSeen = now

	if len(e.Reasons) < 5 {
		e.Reasons = append(e.Reasons, reason)
	}

	if e.Count >= d.threshold && !e.Notified {
		e.Notified = true
		return true, e.Count
	}

	return false, e.Count
}

// RecordSuccess resets loop state for a fingerprint.
func (d *Detector) RecordSuccess(fingerprint string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.entries, fingerprint)
}

// IsLooping reports whether a fingerprint is currently considered a loop.
func (d *Detector) IsLooping(fingerprint string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.entries[fingerprint]
	if !ok {
		return false
	}

	if time.Since(e.LastSeen) > d.window {
		return false
	}

	return e.Count >= d.threshold
}
