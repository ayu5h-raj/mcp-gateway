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
