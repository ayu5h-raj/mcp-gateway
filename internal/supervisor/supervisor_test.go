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
