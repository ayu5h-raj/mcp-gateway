package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustStart(t *testing.T) (*Supervisor, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(SupervisorOpts{LogDir: t.TempDir(), MaxRestartAttempts: 3, BackoffMaxSeconds: 1})
	go s.Run(ctx)
	return s, cancel
}

func waitState(t *testing.T, s *Supervisor, name string, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if snap := s.Status(name); snap.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := s.Status(name)
	t.Fatalf("server %q: wanted state %s, got %s (last err: %v)", name, want, snap.State, snap.LastError)
}

func TestSupervisor_StartsAndTracksServer(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	s.Set("echo", ServerSpec{
		Name:    "echo",
		Command: "sh",
		Args:    []string{"-c", "cat"},
	})
	waitState(t, s, "echo", StateRunning, 2*time.Second)
	require.NotNil(t, s.Process("echo"))
}

func TestSupervisor_RestartsOnCrash(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	// Process that exits immediately so the supervisor observes a crash.
	s.Set("crasher", ServerSpec{
		Name:    "crasher",
		Command: "sh",
		Args:    []string{"-c", "exit 1"},
	})
	// Should cycle through attempts and eventually land in Disabled (maxAttempts=3).
	waitState(t, s, "crasher", StateDisabled, 4*time.Second)
	snap := s.Status("crasher")
	assert.Equal(t, 3, snap.RestartCount)
}

func TestSupervisor_RemoveKillsServer(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	s.Set("ok", ServerSpec{
		Name:    "ok",
		Command: "sh",
		Args:    []string{"-c", "cat"},
	})
	waitState(t, s, "ok", StateRunning, 2*time.Second)
	s.Remove("ok")
	waitState(t, s, "ok", StateStopped, 2*time.Second)
}

func TestSupervisor_RestartsOnSpecChange(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	s.Set("x", ServerSpec{
		Name:    "x",
		Command: "sh",
		Args:    []string{"-c", "cat"},
	})
	waitState(t, s, "x", StateRunning, 2*time.Second)
	first := s.Process("x")
	require.NotNil(t, first)

	// Change spec; supervisor must kill old proc and start a new one.
	s.Set("x", ServerSpec{
		Name:    "x",
		Command: "sh",
		Args:    []string{"-c", "sleep 60; cat"},
	})
	// Wait until the process object changes (proves a new spawn happened).
	deadline := time.Now().Add(4 * time.Second)
	var second *Process
	for time.Now().Before(deadline) {
		second = s.Process("x")
		if second != nil && second != first {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, second, "expected new process after spec change")
	assert.NotSame(t, first, second, "process pointer should change after spec change")
	waitState(t, s, "x", StateRunning, 2*time.Second)
}

func TestSupervisor_BackoffHonoredAcrossUnrelatedWakes(t *testing.T) {
	// Regression: a wake() from an unrelated Set must NOT short-circuit
	// the backoff window for a server currently in StateErrored.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(SupervisorOpts{LogDir: t.TempDir(), MaxRestartAttempts: 10, BackoffMaxSeconds: 60})
	go s.Run(ctx)

	// Fails-to-start-ever server; first retry scheduled at ~now+1s.
	s.Set("bad", ServerSpec{Name: "bad", Command: "sh", Args: []string{"-c", "exit 1"}})
	waitState(t, s, "bad", StateErrored, 2*time.Second)
	before := s.Status("bad").RestartCount

	// Unrelated Set/Remove burst — each wakes the reconcile loop.
	for i := 0; i < 5; i++ {
		s.Set("noise", ServerSpec{Name: "noise", Command: "sh", Args: []string{"-c", "cat"}})
		s.Remove("noise")
	}
	// Restart count must not have jumped by more than 1 during the first ~500ms
	// (the backoff window is ~1s).
	time.Sleep(500 * time.Millisecond)
	snap := s.Status("bad")
	assert.LessOrEqual(t, snap.RestartCount-before, 1,
		"backoff window was defeated: restart count jumped from %d to %d under unrelated wakeups",
		before, snap.RestartCount)
}
