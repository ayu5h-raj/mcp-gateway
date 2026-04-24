package configwrite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

const validConfig = `{
  "version": 1,
  "daemon": { "http_port": 7823, "log_level": "info" },
  "mcpServers": {
    "alpha": { "command": "echo", "enabled": true }
  }
}`

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.jsonc")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestApply_AddsServer(t *testing.T) {
	path := writeTmp(t, validConfig)

	err := Apply(path, func(c *config.Config) error {
		c.MCPServers["beta"] = config.Server{Command: "cat", Enabled: true}
		return nil
	})
	require.NoError(t, err)

	got, err := config.ParseFile(path)
	require.NoError(t, err)
	assert.Contains(t, got.MCPServers, "alpha")
	assert.Contains(t, got.MCPServers, "beta")
}

func TestApply_AtomicViaTempRename(t *testing.T) {
	path := writeTmp(t, validConfig)

	// After Apply, the file at path should still exist (no half-write).
	err := Apply(path, func(c *config.Config) error {
		c.MCPServers["beta"] = config.Server{Command: "cat", Enabled: true}
		return nil
	})
	require.NoError(t, err)

	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, st.IsDir())
}

func TestApply_ValidationFailureLeavesFileUntouched(t *testing.T) {
	path := writeTmp(t, validConfig)
	original, err := os.ReadFile(path)
	require.NoError(t, err)

	err = Apply(path, func(c *config.Config) error {
		// Set an invalid log level — Validate rejects.
		c.Daemon.LogLevel = "chatty"
		return nil
	})
	require.Error(t, err)

	// File on disk is unchanged.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after))
}

func TestApply_MutatorErrorBailsOut(t *testing.T) {
	path := writeTmp(t, validConfig)
	original, err := os.ReadFile(path)
	require.NoError(t, err)

	err = Apply(path, func(*config.Config) error {
		return assert.AnError
	})
	require.Error(t, err)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after))
}

func TestApply_OutputIsValidJSON(t *testing.T) {
	path := writeTmp(t, validConfig)

	err := Apply(path, func(c *config.Config) error {
		c.MCPServers["beta"] = config.Server{Command: "cat", Enabled: true}
		return nil
	})
	require.NoError(t, err)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw any
	require.NoError(t, json.Unmarshal(b, &raw))
}
