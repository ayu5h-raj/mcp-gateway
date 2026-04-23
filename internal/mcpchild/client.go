// Package mcpchild implements an MCP client that speaks JSON-RPC over stdio
// pipes (intended for a supervised child process).
package mcpchild

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Tool is what list_tools returns (kept minimal to avoid SDK coupling).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Resource mirrors the MCP resources/list shape we need.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// Prompt mirrors prompts/list.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument mirrors the prompts/list argument descriptor.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// CallResult is returned from tools/call.
type CallResult struct {
	Content []any `json:"content"`
	IsError bool  `json:"isError"`
}

// Client is an MCP client for one downstream child.
type Client struct {
	Name string

	in  io.WriteCloser
	out io.ReadCloser
	br  *bufio.Reader

	nextID   atomic.Int64
	mu       sync.Mutex
	inflight map[string]chan *rpcResp

	// notify callbacks (set via Subscribe* methods):
	onToolsListChanged     func()
	onResourcesListChanged func()
	onPromptsListChanged   func()
	onResourceUpdated      func(uri string)
}

// New creates a Client bound to a child's stdio.
func New(name string, in io.WriteCloser, out io.ReadCloser) *Client {
	return &Client{
		Name:     name,
		in:       in,
		out:      out,
		br:       bufio.NewReader(out),
		inflight: map[string]chan *rpcResp{},
	}
}

// Initialize performs the MCP initialize handshake and starts the frame reader.
func (c *Client) Initialize(ctx context.Context) error {
	go c.readLoop()
	_, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mcp-gateway", "version": "0.1"},
	})
	if err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

// ListTools calls tools/list.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.request(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Tools, nil
}

// ListResources calls resources/list.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	raw, err := c.request(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Resources []Resource `json:"resources"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Resources, nil
}

// ListPrompts calls prompts/list.
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	raw, err := c.request(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Prompts []Prompt `json:"prompts"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Prompts, nil
}

// CallTool invokes tools/call.
func (c *Client) CallTool(ctx context.Context, name string, args any) (*CallResult, error) {
	raw, err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ReadResource calls resources/read.
func (c *Client) ReadResource(ctx context.Context, uri string) (json.RawMessage, error) {
	return c.request(ctx, "resources/read", map[string]any{"uri": uri})
}

// GetPrompt calls prompts/get.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (json.RawMessage, error) {
	return c.request(ctx, "prompts/get", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

// OnToolsListChanged registers a callback for tools/list_changed notifications.
func (c *Client) OnToolsListChanged(cb func()) { c.onToolsListChanged = cb }

// OnResourcesListChanged registers a callback for resources/list_changed.
func (c *Client) OnResourcesListChanged(cb func()) { c.onResourcesListChanged = cb }

// OnPromptsListChanged registers a callback for prompts/list_changed.
func (c *Client) OnPromptsListChanged(cb func()) { c.onPromptsListChanged = cb }

// OnResourceUpdated registers a callback for resources/updated(uri).
func (c *Client) OnResourceUpdated(cb func(uri string)) { c.onResourceUpdated = cb }

// Close shuts the client (pipes are owned by the supervisor).
func (c *Client) Close() error { return nil }

// ---- internal wire protocol ----

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"` // notifications have no id
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := fmt.Sprintf("%d", c.nextID.Add(1))
	ch := make(chan *rpcResp, 1)
	c.mu.Lock()
	c.inflight[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
	}()

	buf, err := json.Marshal(rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if _, err := c.in.Write(append(buf, '\n')); err != nil {
		return nil, err
	}

	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s (code %d)", method, r.Error.Message, r.Error.Code)
		}
		return r.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	buf, err := json.Marshal(rpcReq{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(buf, '\n'))
	return err
}

func (c *Client) readLoop() {
	for {
		line, err := c.br.ReadBytes('\n')
		if len(line) > 0 {
			var r rpcResp
			if json.Unmarshal(line, &r) == nil {
				c.dispatch(&r)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
	}
}

func (c *Client) dispatch(r *rpcResp) {
	if r.ID != "" {
		c.mu.Lock()
		ch, ok := c.inflight[r.ID]
		c.mu.Unlock()
		if ok {
			ch <- r
		}
		return
	}
	// Notification from the child.
	switch r.Method {
	case "notifications/tools/list_changed":
		if c.onToolsListChanged != nil {
			c.onToolsListChanged()
		}
	case "notifications/resources/list_changed":
		if c.onResourcesListChanged != nil {
			c.onResourcesListChanged()
		}
	case "notifications/prompts/list_changed":
		if c.onPromptsListChanged != nil {
			c.onPromptsListChanged()
		}
	case "notifications/resources/updated":
		if c.onResourceUpdated != nil {
			var p struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(r.Params, &p)
			c.onResourceUpdated(p.URI)
		}
	}
}
