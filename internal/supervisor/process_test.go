package supervisor

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpawn_LifecyclesEcho(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "echo.log")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, err := Spawn(ctx, SpawnConfig{
		Name:       "echo",
		Command:    "sh",
		Args:       []string{"-c", "echo hello; cat"}, // stay alive until stdin closed
		Env:        map[string]string{"FOO": "bar"},
		StderrPath: logPath,
	})
	require.NoError(t, err)

	// Write to stdin and read the echo back on stdout.
	_, err = io.WriteString(p.Stdin, "world\n")
	require.NoError(t, err)

	r := bufio.NewReader(p.Stdout)
	line1, _ := r.ReadString('\n')
	assert.Contains(t, line1, "hello")
	line2, _ := r.ReadString('\n')
	assert.Contains(t, line2, "world")

	// Kill and verify exit.
	require.NoError(t, p.Kill())
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit after Kill")
	}
}

func TestSpawn_CapturesStderrToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "err.log")

	p, err := Spawn(context.Background(), SpawnConfig{
		Name:       "stderrtest",
		Command:    "sh",
		Args:       []string{"-c", "echo boom >&2; sleep 0.1"},
		StderrPath: logPath,
	})
	require.NoError(t, err)
	<-p.Done()

	buf, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(buf), "boom"), "stderr: %q", string(buf))
}

func TestSpawn_MissingCommandErrors(t *testing.T) {
	_, err := Spawn(context.Background(), SpawnConfig{
		Name:    "nope",
		Command: "definitely-not-a-real-command-for-test",
	})
	require.Error(t, err)
}
