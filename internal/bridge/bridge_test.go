package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeHTTP() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"method": req.Method, "ok": true},
		})
	})
	return httptest.NewServer(mux)
}

func TestBridge_ProxiesOneRequestResponse(t *testing.T) {
	srv := fakeHTTP()
	defer srv.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"initialize"}` + "\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Run(ctx, RunConfig{
		URL:    srv.URL + "/mcp",
		Stdin:  in,
		Stdout: &out,
	})
	require.NoError(t, err)
	line, err := readLine(&out)
	require.NoError(t, err)
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &resp))
	// id may come back as a string here because the fake echoes the raw JSON id
	assert.Equal(t, "1", strings.Trim(string(toJSON(resp["id"])), `"`))
	result := resp["result"].(map[string]any)
	assert.Equal(t, "initialize", result["method"])
	assert.Equal(t, true, result["ok"])
}

func toJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func readLine(r io.Reader) (string, error) {
	var buf bytes.Buffer
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				return buf.String(), nil
			}
			buf.WriteByte(b[0])
		}
		if err != nil {
			return buf.String(), err
		}
	}
}

// Regression: a 202 response with empty body (the daemon's notification ack)
// must NOT result in an empty/garbage line on stdout. Otherwise the
// downstream MCP client will fail JSON-RPC validation.
func TestBridge_NotificationAckProducesNoStdoutFrame(t *testing.T) {
	mux := http.NewServeMux()
	var calls int
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	in := strings.NewReader(
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"x"}}` + "\n",
	)
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, Run(ctx, RunConfig{
		URL:    srv.URL + "/mcp",
		Stdin:  in,
		Stdout: &out,
	}))
	assert.Equal(t, 2, calls, "both notifications must reach the daemon")
	assert.Empty(t, out.Bytes(), "notifications must produce zero stdout bytes; got %q", out.String())
}

// Mixed sequence: notification, request, notification, request — stdout must
// contain exactly two response frames in the right order.
func TestBridge_InterleavedRequestsAndNotifications(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.ID) == 0 || strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"method": req.Method},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"2"}}`,
	}
	in := strings.NewReader(strings.Join(frames, "\n") + "\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, Run(ctx, RunConfig{URL: srv.URL + "/mcp", Stdin: in, Stdout: &out}))

	// Should see exactly 2 response lines (one for initialize, one for tools/list).
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.Len(t, got, 2, "expected 2 response frames, got %d: %v", len(got), got)
	for _, line := range got {
		var resp map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &resp), "invalid JSON: %s", line)
		assert.NotNil(t, resp["id"], "every response must have an id; got %s", line)
		assert.NotNil(t, resp["result"], "every response must have a result; got %s", line)
	}
}
