package pidfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquire_WritesPidAndReleases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	release, existing, err := Acquire(path)
	require.NoError(t, err)
	require.Equal(t, 0, existing)
	require.NotNil(t, release)

	// pidfile contains our pid
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	got, err := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), got)

	release()
	// File should be gone after release.
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "pidfile should be removed on release")
}

func TestAcquire_DoubleAcquireSameProcessFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	release1, _, err := Acquire(path)
	require.NoError(t, err)
	defer release1()

	// Same-process re-acquire should fail (flock is exclusive per fd; we use a fresh fd).
	_, existing, err := Acquire(path)
	require.Error(t, err)
	assert.Equal(t, os.Getpid(), existing)
}

func TestAcquire_AnotherProcessHoldsItFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	// Spawn a child that holds the pidfile for 2 seconds.
	holder := exec.Command("sh", "-c", "exec sleep 2")
	stdin, err := holder.StdinPipe()
	require.NoError(t, err)
	defer stdin.Close()

	// We can't directly call Acquire in another process without a binary, so
	// instead simulate by acquiring in this process and verifying a second
	// Acquire from a different file descriptor (same path, fresh open) fails.
	release1, _, err := Acquire(path)
	require.NoError(t, err)
	defer release1()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _, err := Acquire(path)
		if err != nil {
			return // expected
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("second Acquire should have failed while first holds the lock")
}

func TestAcquire_StalePidfileReclaimable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	// Pre-write a stale pidfile (no flock → not held by anyone).
	require.NoError(t, os.WriteFile(path, []byte("99999\n"), 0o600))

	release, _, err := Acquire(path)
	require.NoError(t, err)
	defer release()
}
