package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

// tempHome creates a short-path temp directory suitable for a daemon home.
// macOS limits AF_UNIX socket paths to 103 characters (104 bytes incl. NUL),
// so we use os.MkdirTemp with a short prefix under /tmp to keep total path
// length well under the limit.
func tempHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mgd")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writeMinimalConfig writes a minimal valid config.jsonc to home.
func writeMinimalConfig(t *testing.T, home string, port int) {
	t.Helper()
	cfg := `{"version":1,"daemon":{"http_port":` + strconv.Itoa(port) + `,"log_level":"info"},"mcpServers":{}}`
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.jsonc"), []byte(cfg), 0o600))
}

// waitForSock polls until home/sock appears or timeout, failing the test.
func waitForSock(t *testing.T, sockPath string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for UNIX socket: %s (len=%d)", sockPath, len(sockPath))
}

// unixClient returns an http.Client that dials the given UNIX socket.
func unixClient(sock string) *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

// TestDaemon_PidfileRefusesDoubleStart starts one daemon, then attempts a
// second Run against the same home directory and expects an error containing
// "already running".
func TestDaemon_PidfileRefusesDoubleStart(t *testing.T) {
	home := tempHome(t)
	writeMinimalConfig(t, home, 17930)
	sock := filepath.Join(home, "sock")

	ctx1, cancel1 := context.WithCancel(context.Background())
	d1 := New(home, nil)
	errCh1 := make(chan error, 1)
	go func() { errCh1 <- d1.Run(ctx1) }()
	t.Cleanup(func() {
		cancel1()
		<-errCh1
	})

	// Wait for the first daemon to be fully up.
	waitForSock(t, sock)

	// Second daemon — same home — must fail immediately.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	d2 := New(home, nil)
	err := d2.Run(ctx2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
}

// TestDaemon_AdminNotOnTCP verifies that the /admin/* endpoints are NOT
// served on the TCP listener (returns non-200) but ARE served on the UNIX
// socket listener (returns 200).
func TestDaemon_AdminNotOnTCP(t *testing.T) {
	home := tempHome(t)
	const port = 17931
	writeMinimalConfig(t, home, port)
	sock := filepath.Join(home, "sock")

	ctx, cancel := context.WithCancel(context.Background())
	d := New(home, nil)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	waitForSock(t, sock)

	// TCP: /admin/status must NOT return 200 (TCP mux only has /mcp).
	tcpURL := "http://127.0.0.1:" + strconv.Itoa(port) + "/admin/status"
	resp, err := http.Get(tcpURL)
	require.NoError(t, err)
	resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode,
		"/admin/status should NOT be served on TCP listener")

	// UNIX socket: /admin/status must return 200.
	client := unixClient(sock)
	resp2, err := client.Get("http://x/admin/status")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"/admin/status should be served on UNIX socket")
}

// TestDaemon_PublishesMCPRequestEvents starts a daemon, subscribes to the
// event bus, POSTs a tools/list request over the UNIX socket, and asserts
// that mcp.request and mcp.response events appear on the bus.
func TestDaemon_PublishesMCPRequestEvents(t *testing.T) {
	home := tempHome(t)
	const port = 17932
	writeMinimalConfig(t, home, port)
	sock := filepath.Join(home, "sock")

	ctx, cancel := context.WithCancel(context.Background())
	d := New(home, nil)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	waitForSock(t, sock)

	// Subscribe before issuing the request.
	ch, unsub := d.events.Subscribe()
	defer unsub()

	// POST tools/list over the UNIX socket.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "t1", "method": "tools/list",
	})
	client := unixClient(sock)
	resp, err := client.Post("http://x/mcp", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Collect events with a short deadline.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	sawRequest := false
	sawResponse := false
	for !sawRequest || !sawResponse {
		select {
		case ev := <-ch:
			if ev.Kind == event.KindMCPRequest && ev.Method == "tools/list" {
				sawRequest = true
			}
			if ev.Kind == event.KindMCPResponse && ev.Method == "tools/list" {
				sawResponse = true
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for mcp.request/mcp.response events; sawRequest=%v sawResponse=%v",
				sawRequest, sawResponse)
		}
	}
}
