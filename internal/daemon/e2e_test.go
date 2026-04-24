//go:build e2e

package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moduleRoot walks up from the current working directory until it finds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err, "getwd")
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod from", dir)
		}
		dir = parent
	}
}

func TestE2E_DaemonServesToolsFromChild(t *testing.T) {
	root := moduleRoot(t)
	tmp := t.TempDir()
	childBin := filepath.Join(tmp, "fakechild")
	buildCmd := exec.Command("go", "build", "-o", childBin, "./internal/testutil/fakechild/cmd/fakechildbin")
	buildCmd.Dir = root
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "go build fakechild: %s", string(out))

	gatewayBin := filepath.Join(tmp, "mcp-gateway")
	buildCmd2 := exec.Command("go", "build", "-o", gatewayBin, "./cmd/mcp-gateway")
	buildCmd2.Dir = root
	out, err = buildCmd2.CombinedOutput()
	require.NoError(t, err, "go build gateway: %s", string(out))

	home := filepath.Join(tmp, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	cfg := fmt.Sprintf(`{
		"version": 1,
		"daemon": { "http_port": 17902, "log_level": "info" },
		"mcpServers": {
			"fake": { "command": %q, "enabled": true }
		}
	}`, childBin)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.jsonc"), []byte(cfg), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gatewayBin, "daemon", "--home", home)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	var tools []any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body := strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"tools/list"}`)
		req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:17902/mcp", body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var r struct {
				Result struct {
					Tools []any `json:"tools"`
				} `json:"result"`
			}
			if json.Unmarshal(b, &r) == nil && len(r.Result.Tools) > 0 {
				tools = r.Result.Tools
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, tools, "tools/list returned empty after timeout")
	first := tools[0].(map[string]any)
	assert.Equal(t, "fake__ping", first["name"])

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tools/call",
		"params": map[string]any{"name": "fake__ping", "arguments": map[string]any{}},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:17902/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var r struct {
		Result struct {
			Content []map[string]any `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(b, &r))
	require.Len(t, r.Result.Content, 1)
	assert.Equal(t, "pong", r.Result.Content[0]["text"])
}
