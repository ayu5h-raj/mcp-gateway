package aggregator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ayu5h-raj/mcp-gateway/internal/mcpchild"
)

// Aggregator merges tools/resources/prompts from N child MCP clients and
// exposes the merged views upstream. Server key is the server prefix.
type Aggregator struct {
	mu        sync.RWMutex
	servers   map[string]*mcpchild.Client // key = prefix
	tools     []Tool                      // merged, sorted by prefixed name
	resources []Resource
	prompts   []Prompt

	// subscriber callbacks (fire synchronously; keep them cheap)
	onToolsChanged     []func()
	onResourcesChanged []func()
	onPromptsChanged   []func()
}

// Tool is an aggregator-level tool (prefixed name + origin).
type Tool struct {
	Name        string // prefixed, e.g. "github__create_issue"
	Description string
	InputSchema []byte
	Server      string
}

// Resource is aggregator-level.
type Resource struct {
	URI         string // prefixed
	Name        string
	Description string
	MimeType    string
	Server      string
}

// Prompt is aggregator-level.
type Prompt struct {
	Name        string // prefixed
	Description string
	Arguments   []PromptArgument
	Server      string
}

// PromptArgument mirrors the mcpchild shape (kept lightweight here).
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// New returns an empty Aggregator.
func New() *Aggregator {
	return &Aggregator{servers: map[string]*mcpchild.Client{}}
}

// AddServer wires a prefix → client mapping and subscribes to list_changed.
// Does not refresh lists; call RefreshAll (or Refresh) after adding.
func (a *Aggregator) AddServer(prefix string, c *mcpchild.Client) {
	a.mu.Lock()
	a.servers[prefix] = c
	a.mu.Unlock()
	c.OnToolsListChanged(func() { _ = a.RefreshTools(context.Background()) })
	c.OnResourcesListChanged(func() { _ = a.RefreshResources(context.Background()) })
	c.OnPromptsListChanged(func() { _ = a.RefreshPrompts(context.Background()) })
}

// RemoveServer drops the prefix. Calls list-changed callbacks.
func (a *Aggregator) RemoveServer(prefix string) {
	a.mu.Lock()
	delete(a.servers, prefix)
	a.mu.Unlock()
	_ = a.RefreshTools(context.Background())
	_ = a.RefreshResources(context.Background())
	_ = a.RefreshPrompts(context.Background())
}

// RefreshAll refreshes tools, resources, and prompts.
func (a *Aggregator) RefreshAll(ctx context.Context) error {
	if err := a.RefreshTools(ctx); err != nil {
		return err
	}
	if err := a.RefreshResources(ctx); err != nil {
		return err
	}
	return a.RefreshPrompts(ctx)
}

// RefreshTools rebuilds the merged tool list and emits a change callback.
func (a *Aggregator) RefreshTools(ctx context.Context) error {
	a.mu.RLock()
	servers := snapshotServers(a.servers)
	a.mu.RUnlock()

	var merged []Tool
	for prefix, c := range servers {
		tools, err := c.ListTools(ctx)
		if err != nil {
			return fmt.Errorf("aggregator: tools/list %s: %w", prefix, err)
		}
		for _, t := range tools {
			merged = append(merged, Tool{
				Name:        PrefixTool(prefix, t.Name),
				Description: t.Description,
				InputSchema: []byte(t.InputSchema),
				Server:      prefix,
			})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })

	a.mu.Lock()
	a.tools = merged
	cbs := append([]func(){}, a.onToolsChanged...)
	a.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return nil
}

// RefreshResources rebuilds the merged resource list.
func (a *Aggregator) RefreshResources(ctx context.Context) error {
	a.mu.RLock()
	servers := snapshotServers(a.servers)
	a.mu.RUnlock()

	var merged []Resource
	for prefix, c := range servers {
		rs, err := c.ListResources(ctx)
		if err != nil {
			// Many MCP servers don't implement resources. "Method not found"
			// (JSON-RPC -32601) is expected and ignored; any other error is
			// propagated so a transient child failure doesn't silently zero
			// the aggregated list.
			if isMethodNotFound(err) {
				continue
			}
			return fmt.Errorf("aggregator: resources/list %s: %w", prefix, err)
		}
		for _, r := range rs {
			merged = append(merged, Resource{
				URI:         PrefixResourceURI(prefix, r.URI),
				Name:        r.Name,
				Description: r.Description,
				MimeType:    r.MimeType,
				Server:      prefix,
			})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].URI < merged[j].URI })

	a.mu.Lock()
	a.resources = merged
	cbs := append([]func(){}, a.onResourcesChanged...)
	a.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return nil
}

// RefreshPrompts rebuilds the merged prompt list.
func (a *Aggregator) RefreshPrompts(ctx context.Context) error {
	a.mu.RLock()
	servers := snapshotServers(a.servers)
	a.mu.RUnlock()

	var merged []Prompt
	for prefix, c := range servers {
		ps, err := c.ListPrompts(ctx)
		if err != nil {
			// Same semantics as resources: tolerate unimplemented, propagate real errors.
			if isMethodNotFound(err) {
				continue
			}
			return fmt.Errorf("aggregator: prompts/list %s: %w", prefix, err)
		}
		for _, p := range ps {
			args := make([]PromptArgument, len(p.Arguments))
			for i, arg := range p.Arguments {
				args[i] = PromptArgument{Name: arg.Name, Description: arg.Description, Required: arg.Required}
			}
			merged = append(merged, Prompt{
				Name:        PrefixTool(prefix, p.Name),
				Description: p.Description,
				Arguments:   args,
				Server:      prefix,
			})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })

	a.mu.Lock()
	a.prompts = merged
	cbs := append([]func(){}, a.onPromptsChanged...)
	a.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return nil
}

// Tools returns a snapshot of the merged tool list.
func (a *Aggregator) Tools() []Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Tool(nil), a.tools...)
}

// Resources returns a snapshot of the merged resource list.
func (a *Aggregator) Resources() []Resource {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Resource(nil), a.resources...)
}

// Prompts returns a snapshot of the merged prompt list.
func (a *Aggregator) Prompts() []Prompt {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Prompt(nil), a.prompts...)
}

// CallTool routes a prefixed tool name to the correct child.
func (a *Aggregator) CallTool(ctx context.Context, prefixedName string, args any) (*mcpchild.CallResult, error) {
	prefix, original, ok := ParsePrefixed(prefixedName)
	if !ok {
		return nil, fmt.Errorf("tool %q has no valid prefix", prefixedName)
	}
	a.mu.RLock()
	c, ok := a.servers[prefix]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no server registered for prefix %q", prefix)
	}
	return c.CallTool(ctx, original, args)
}

// ReadResource routes resources/read to the correct child.
func (a *Aggregator) ReadResource(ctx context.Context, prefixedURI string) ([]byte, error) {
	prefix, original, ok := ParsePrefixedResourceURI(prefixedURI)
	if !ok {
		return nil, fmt.Errorf("resource URI %q has no valid prefix", prefixedURI)
	}
	a.mu.RLock()
	c, ok := a.servers[prefix]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no server registered for prefix %q", prefix)
	}
	raw, err := c.ReadResource(ctx, original)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// GetPrompt routes prompts/get to the correct child.
func (a *Aggregator) GetPrompt(ctx context.Context, prefixedName string, args map[string]string) ([]byte, error) {
	prefix, original, ok := ParsePrefixed(prefixedName)
	if !ok {
		return nil, fmt.Errorf("prompt %q has no valid prefix", prefixedName)
	}
	a.mu.RLock()
	c, ok := a.servers[prefix]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no server registered for prefix %q", prefix)
	}
	raw, err := c.GetPrompt(ctx, original, args)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// OnToolsChanged registers a callback fired after tools rebuild.
func (a *Aggregator) OnToolsChanged(cb func()) {
	a.mu.Lock()
	a.onToolsChanged = append(a.onToolsChanged, cb)
	a.mu.Unlock()
}

// OnResourcesChanged registers a callback fired after resources rebuild.
func (a *Aggregator) OnResourcesChanged(cb func()) {
	a.mu.Lock()
	a.onResourcesChanged = append(a.onResourcesChanged, cb)
	a.mu.Unlock()
}

// OnPromptsChanged registers a callback fired after prompts rebuild.
func (a *Aggregator) OnPromptsChanged(cb func()) {
	a.mu.Lock()
	a.onPromptsChanged = append(a.onPromptsChanged, cb)
	a.mu.Unlock()
}

// snapshotServers returns a shallow copy of the servers map for
// read-only iteration outside the RWMutex.
func snapshotServers(m map[string]*mcpchild.Client) map[string]*mcpchild.Client {
	cp := make(map[string]*mcpchild.Client, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// isMethodNotFound reports whether err represents a JSON-RPC "method not found"
// (code -32601). The mcpchild client formats errors as "<method>: <msg> (code <n>)",
// so we match on the trailing numeric code fragment.
func isMethodNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "code -32601")
}
