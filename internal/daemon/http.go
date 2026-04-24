package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
)

// NewMCPHandler returns an http.Handler that implements the POST /mcp half of
// the Streamable HTTP transport. All JSON-RPC requests must be POSTs; the body
// is a single JSON-RPC request; response is a single JSON-RPC response (or
// HTTP 202 with no body if the request was a notification).
// Server-initiated streams (SSE on GET /mcp) are out of scope for v0.
func NewMCPHandler(agg *aggregator.Aggregator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req rpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			writeErr(w, nil, -32700, "parse error: "+err.Error())
			return
		}
		// Notifications (no id, or method starting with "notifications/") MUST NOT
		// receive a response per JSON-RPC 2.0. Acknowledge with 202 and no body.
		if isNotification(req) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		resp := dispatch(ctx, agg, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// isNotification reports whether the request is a JSON-RPC notification —
// either the method is in the MCP "notifications/" namespace, or the request
// has no id. Notifications must not receive a response.
func isNotification(req rpcReq) bool {
	if strings.HasPrefix(req.Method, "notifications/") {
		return true
	}
	// Empty/absent id → notification. Note: explicit `null` id is technically
	// a request per JSON-RPC, so distinguish it from absent.
	if len(req.ID) == 0 {
		return true
	}
	return false
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func dispatch(ctx context.Context, agg *aggregator.Aggregator, req rpcReq) rpcResp {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{"listChanged": true},
				"prompts":   map[string]any{"listChanged": true},
				"logging":   map[string]any{},
			},
			"serverInfo": map[string]any{"name": "mcp-gateway", "version": "0.1"},
		})
	case "ping":
		return ok(req.ID, map[string]any{})
	case "tools/list":
		tools := agg.Tools()
		out := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			out = append(out, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": json.RawMessage(t.InputSchema),
			})
		}
		return ok(req.ID, map[string]any{"tools": out})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params: "+err.Error())
		}
		var args any
		if len(p.Arguments) > 0 {
			_ = json.Unmarshal(p.Arguments, &args)
		}
		res, err := agg.CallTool(ctx, p.Name, args)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		return ok(req.ID, map[string]any{"content": res.Content, "isError": res.IsError})
	case "resources/list":
		out := make([]map[string]any, 0)
		for _, r := range agg.Resources() {
			out = append(out, map[string]any{
				"uri":         r.URI,
				"name":        r.Name,
				"description": r.Description,
				"mimeType":    r.MimeType,
			})
		}
		return ok(req.ID, map[string]any{"resources": out})
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params: "+err.Error())
		}
		raw, err := agg.ReadResource(ctx, p.URI)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		var payload any
		_ = json.Unmarshal(raw, &payload)
		return ok(req.ID, payload)
	case "prompts/list":
		out := make([]map[string]any, 0)
		for _, p := range agg.Prompts() {
			args := make([]map[string]any, 0, len(p.Arguments))
			for _, a := range p.Arguments {
				args = append(args, map[string]any{
					"name":        a.Name,
					"description": a.Description,
					"required":    a.Required,
				})
			}
			out = append(out, map[string]any{
				"name":        p.Name,
				"description": p.Description,
				"arguments":   args,
			})
		}
		return ok(req.ID, map[string]any{"prompts": out})
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params: "+err.Error())
		}
		raw, err := agg.GetPrompt(ctx, p.Name, p.Arguments)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		var payload any
		_ = json.Unmarshal(raw, &payload)
		return ok(req.ID, payload)
	default:
		return fail(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func ok(id json.RawMessage, result any) rpcResp {
	return rpcResp{JSONRPC: "2.0", ID: id, Result: result}
}
func fail(id json.RawMessage, code int, msg string) rpcResp {
	return rpcResp{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}
func writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	_ = json.NewEncoder(w).Encode(fail(id, code, msg))
}
