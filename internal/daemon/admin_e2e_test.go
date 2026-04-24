//go:build e2e

package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayu5h-raj/mcp-gateway/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unixHTTPClient(sock string) *http.Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func TestE2E_AdminStatusOverUnix(t *testing.T) {
	root := moduleRoot(t)
	tmp := t.TempDir()
	gatewayBin := filepath.Join(tmp, "mcp-gateway")
	buildCmd := exec.Command("go", "build", "-o", gatewayBin, "./cmd/mcp-gateway")
	buildCmd.Dir = root
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "go build: %s", string(out))

	home := filepath.Join(tmp, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	cfg := `{"version":1,"daemon":{"http_port":17923,"log_level":"info"},"mcpServers":{}}`
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

	sock := daemon.ChooseSocketPath(home)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	c := unixHTTPClient(sock)
	resp, err := c.Get("http://x/admin/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.NotZero(t, st["pid"])
	assert.EqualValues(t, 17923, st["http_port"])
	assert.Equal(t, "0.2", st["version"])
}

func TestE2E_AdminNotOnTCP(t *testing.T) {
	root := moduleRoot(t)
	tmp := t.TempDir()
	gatewayBin := filepath.Join(tmp, "mcp-gateway")
	buildCmd := exec.Command("go", "build", "-o", gatewayBin, "./cmd/mcp-gateway")
	buildCmd.Dir = root
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "go build: %s", string(out))

	home := filepath.Join(tmp, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.jsonc"),
		[]byte(`{"version":1,"daemon":{"http_port":17924,"log_level":"info"},"mcpServers":{}}`), 0o600))

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
	time.Sleep(1500 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:17924/admin/status")
	if err == nil {
		defer resp.Body.Close()
		// Must NOT return 200 — admin path is not registered on TCP mux.
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	}
}
