// Package fakechild is a tiny in-memory stdio MCP server used in tests.
// It implements the minimum protocol surface for initialize/tools.list/tools.call.
// Kept hand-rolled (no SDK dependency in the test child binary) so we're testing
// our adapter against plain JSON-RPC frames, not the SDK against itself.
package fakechild

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Tool is a minimal schema for tools this fake advertises.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Server is a stdio JSON-RPC server.
type Server struct {
	mu    sync.Mutex
	tools []Tool
	// onCall is called when tools/call fires; should return (content, isError).
	onCall func(name string, args json.RawMessage) ([]any, bool)
}

// New creates a Server with the given tools and tool-call handler.
func New(tools []Tool, onCall func(string, json.RawMessage) ([]any, bool)) *Server {
	return &Server{tools: tools, onCall: onCall}
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
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads newline-delimited JSON-RPC frames from in and writes responses to out.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	bw := bufio.NewWriter(out)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var req rpcReq
			if e := json.Unmarshal(line, &req); e != nil {
				continue
			}
			resp := s.handle(req)
			if resp != nil {
				b, _ := json.Marshal(resp)
				_, _ = bw.Write(b)
				_ = bw.WriteByte('\n')
				_ = bw.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handle(req rpcReq) *rpcResp {
	switch req.Method {
	case "initialize":
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{"name": "fakechild", "version": "0.0.1"},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil // no response to notifications
	case "tools/list":
		s.mu.Lock()
		out := make([]Tool, len(s.tools))
		copy(out, s.tools)
		s.mu.Unlock()
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": out}}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		content, isErr := []any{}, false
		if s.onCall != nil {
			content, isErr = s.onCall(params.Name, params.Arguments)
		}
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": content,
			"isError": isErr,
		}}
	default:
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Error: &rpcErr{
			Code:    -32601,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		}}
	}
}

// StringContent returns a text content block.
func StringContent(s string) map[string]any {
	return map[string]any{"type": "text", "text": s}
}

// MustRaw marshals v or panics — handy in tests.
func MustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Keep strings import tidy if the package stops using it.
var _ = strings.TrimSpace
