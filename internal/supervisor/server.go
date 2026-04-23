package supervisor

import (
	"time"
)

// State is the lifecycle stage of a supervised child process.
type State int

const (
	StateStarting State = iota
	StateRunning
	StateErrored
	StateRestarting
	StateDisabled
	StateStopped
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateErrored:
		return "errored"
	case StateRestarting:
		return "restarting"
	case StateDisabled:
		return "disabled"
	case StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Backoff is exponential with a hard cap (1, 2, 4, 8, 16, ... maxSeconds).
type Backoff struct {
	max     time.Duration
	attempt int
}

// NewBackoff creates a Backoff capped at maxSeconds seconds.
func NewBackoff(maxSeconds int) *Backoff {
	return &Backoff{max: time.Duration(maxSeconds) * time.Second}
}

// Next returns the next delay and advances.
func (b *Backoff) Next() time.Duration {
	b.attempt++
	// 2^(attempt-1) seconds; first call → 1s.
	shift := b.attempt - 1
	if shift > 30 {
		shift = 30
	}
	d := time.Duration(1<<shift) * time.Second
	if d > b.max || d < 0 {
		d = b.max
	}
	return d
}

// Reset clears the attempt counter (call after a clean run of sufficient duration).
func (b *Backoff) Reset() { b.attempt = 0 }
