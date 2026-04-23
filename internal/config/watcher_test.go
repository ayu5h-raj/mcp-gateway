package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validJSONC = `{
	"version": 1,
	"daemon": { "http_port": 7823, "log_level": "info" },
	"mcpServers": { "%s": { "command": "echo", "enabled": true } }
}`

func writeCfg(t *testing.T, dir string, body string) string {
	t.Helper()
	p := filepath.Join(dir, "config.jsonc")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestWatcher_EmitsInitialLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, fmt.Sprintf(validJSONC, "a"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Close()

	select {
	case cfg := <-w.Changes():
		require.Contains(t, cfg.MCPServers, "a")
	case <-ctx.Done():
		t.Fatal("no initial config received")
	}
}

func TestWatcher_EmitsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, fmt.Sprintf(validJSONC, "a"))

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Close()

	// Drain initial.
	<-w.Changes()

	// Atomic rewrite: write-temp + rename. Mirrors how CLI mutations will write.
	tmp := path + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(fmt.Sprintf(validJSONC, "b")), 0o600))
	require.NoError(t, os.Rename(tmp, path))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case cfg := <-w.Changes():
		assert.Contains(t, cfg.MCPServers, "b")
	case <-ctx.Done():
		t.Fatal("no change emitted after rename")
	}
}
