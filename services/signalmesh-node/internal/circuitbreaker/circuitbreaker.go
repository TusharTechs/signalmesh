package circuitbreaker

import (
	"sync"
	"time"
)

type State string

const (
	StateClosed   State = "CLOSED"
	StateOpen     State = "OPEN"
	StateHalfOpen State = "HALF_OPEN"
)

type Config struct {
	FailureThreshold         int
	Cooldown                 time.Duration
	HalfOpenSuccessThreshold int
	HalfOpenMaxProbes        int
}

func DefaultConfig() Config {
	return Config{
		FailureThreshold:         3,
		Cooldown:                 5 * time.Second,
		HalfOpenSuccessThreshold: 1,
		HalfOpenMaxProbes:        1,
	}
}

// Breaker is a concurrency-safe circuit breaker state machine.
type Breaker struct {
	mu              sync.Mutex
	cfg             Config
	state           State
	failures        int
	successes       int
	halfOpenProbes  int
	lastStateChange time.Time
}

func New(cfg Config) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}

	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 5 * time.Second
	}

	if cfg.HalfOpenSuccessThreshold <= 0 {
		cfg.HalfOpenSuccessThreshold = 1
	}

	if cfg.HalfOpenMaxProbes <= 0 {
		cfg.HalfOpenMaxProbes = 1
	}

	return &Breaker{
		cfg:             cfg,
		state:           StateClosed,
		lastStateChange: time.Now(),
	}
}

// Available reports whether the breaker may allow a request.
// It does not consume a half-open probe.
func (b *Breaker) Available() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		return now.Sub(b.lastStateChange) >= b.cfg.Cooldown
	case StateHalfOpen:
		return b.halfOpenProbes < b.cfg.HalfOpenMaxProbes
	default:
		return false
	}
}

// Allow reports whether the request may proceed and consumes a half-open probe
// if necessary.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(b.lastStateChange) >= b.cfg.Cooldown {
			b.setState(StateHalfOpen)
			b.halfOpenProbes = 1
			return true
		}
		return false
	case StateHalfOpen:
		if b.halfOpenProbes < b.cfg.HalfOpenMaxProbes {
			b.halfOpenProbes++
			return true
		}
		return false
	default:
		return false
	}
}

func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures = 0
	case StateHalfOpen:
		b.successes++
		if b.successes >= b.cfg.HalfOpenSuccessThreshold {
			b.setState(StateClosed)
		}
	default:
	}
}

func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.cfg.FailureThreshold {
			b.setState(StateOpen)
		}
	case StateHalfOpen:
		b.setState(StateOpen)
	default:
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) setState(state State) {
	b.state = state
	b.lastStateChange = time.Now()
	b.failures = 0
	b.successes = 0
	b.halfOpenProbes = 0
}

// Reset returns the breaker to CLOSED. Used when an operator (or a chaos
// scenario restore) declares the underlying dependency healthy again.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != StateClosed {
		b.setState(StateClosed)
	}
}

// Trip forces the breaker into the OPEN state.
// This is used when SignalMesh detects an agent-level incident such as a retry loop.
func (b *Breaker) Trip() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != StateOpen {
		b.setState(StateOpen)
	}
}
