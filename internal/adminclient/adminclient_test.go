package adminclient

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unixMaxPath = 103

var testCounter atomic.Uint64

func startUnixServer(t *testing.T, h http.Handler) string {
	t.Helper()

	// Choose a short enough socket path for macOS AF_UNIX (103-char limit).
	sock := fmt.Sprintf("/tmp/mgw-client-test-%d-%d.sock", os.Getpid(), testCounter.Add(1))
	if natural := t.TempDir() + "/sock"; len(natural) <= unixMaxPath {
		sock = natural
	}

	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(sock, 0o600))
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close(); _ = os.Remove(sock) })
	return sock
}

func TestClient_GetJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "n": 7})
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	var got map[string]any
	require.NoError(t, c.Get("/admin/status", &got))
	assert.Equal(t, true, got["ok"])
	assert.EqualValues(t, 7, got["n"].(float64))
}

func TestClient_PostJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/secret/x", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	require.NoError(t, c.Post("/admin/secret/x", map[string]string{"value": "y"}, nil))
}

func TestClient_Delete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/servers/foo", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	require.NoError(t, c.Delete("/admin/servers/foo"))
}

func TestClient_NonOKReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/x", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	err := c.Get("/admin/x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}
