package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/mcpchild"
	"github.com/ayu5h-raj/mcp-gateway/internal/testutil/fakechild"
)

func setupAggregator(t *testing.T) *aggregator.Aggregator {
	t.Helper()
	agg := aggregator.New()
	tools := []fakechild.Tool{{Name: "ping", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := fakechild.New(tools, func(name string, _ json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("pong")}, false
	})
	go func() { _ = srv.Serve(inR, outW); outW.Close() }()
	c := mcpchild.New("a", inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))
	agg.AddServer("alpha", c)
	require.NoError(t, agg.RefreshAll(ctx))
	return agg
}

func postJSON(t *testing.T, h http.Handler, body any) map[string]any {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestMCP_Initialize(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	result, ok := out["result"].(map[string]any)
	require.True(t, ok)
	caps, ok := result["capabilities"].(map[string]any)
	require.True(t, ok)
	_, hasTools := caps["tools"]
	assert.True(t, hasTools)
}

func TestMCP_ToolsList(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tools/list",
	})
	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)
	require.Len(t, tools, 1)
	first := tools[0].(map[string]any)
	assert.Equal(t, "alpha__ping", first["name"])
}

func TestMCP_ToolsCall(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "3", "method": "tools/call",
		"params": map[string]any{"name": "alpha__ping", "arguments": map[string]any{}},
	})
	result := out["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	assert.Equal(t, "pong", first["text"])
}

func TestMCP_RejectsUnknownMethod(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "4", "method": "no/such/method",
	})
	errObj, ok := out["error"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, -32601, errObj["code"])
}

func TestMCP_RejectsNonPost(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// Regression for the bug where notifications got a JSON-RPC response and
// Claude Desktop's validator rejected the malformed frame. Per JSON-RPC 2.0,
// notifications (no id, or method in the "notifications/" namespace) MUST NOT
// receive a response. We acknowledge with HTTP 202 and an empty body.
func TestMCP_NotificationGets202NoBody(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	cases := []map[string]any{
		// Canonical: method in notifications/ namespace, no id.
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		// notifications/cancelled — also a notification.
		{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": "1"}},
		// A request with no id is also a notification.
		{"jsonrpc": "2.0", "method": "tools/list"},
	}
	for _, c := range cases {
		buf, _ := json.Marshal(c)
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusAccepted, rec.Code, "case=%v", c)
		assert.Empty(t, rec.Body.Bytes(), "notification must produce no body; case=%v", c)
	}
}

// Sanity: ping is a request (has an id) and gets a real response.
func TestMCP_PingGetsResponse(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg, event.New(64))
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "p1", "method": "ping",
	})
	require.NotNil(t, out["result"])
	assert.Nil(t, out["error"])
}
