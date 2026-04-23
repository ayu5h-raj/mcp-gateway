package mcpchild

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayushraj/mcp-gateway/internal/testutil/fakechild"
)

// newPipedChild creates a goroutine-hosted fake stdio MCP server and returns
// in-memory stdin/stdout pipes connecting to it, plus a cleanup function.
func newPipedChild(t *testing.T, tools []fakechild.Tool,
	onCall func(string, json.RawMessage) ([]any, bool),
) (stdin io.WriteCloser, stdout io.ReadCloser, cleanup func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := fakechild.New(tools, onCall)
	done := make(chan struct{})
	go func() {
		_ = s.Serve(inR, outW)
		_ = outW.Close()
		close(done)
	}()
	cleanup = func() {
		_ = inW.Close()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
	return inW, outR, cleanup
}

func TestClient_InitializeAndListTools(t *testing.T) {
	tools := []fakechild.Tool{
		{Name: "hello", Description: "say hi", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
		{Name: "world", Description: "say world", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	}
	in, out, cleanup := newPipedChild(t, tools, nil)
	defer cleanup()

	c := New("fake", in, out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	got, err := c.ListTools(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	names := []string{got[0].Name, got[1].Name}
	assert.ElementsMatch(t, []string{"hello", "world"}, names)
}

func TestClient_CallTool(t *testing.T) {
	tools := []fakechild.Tool{{Name: "echo", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})}}
	onCall := func(name string, args json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("ok:" + string(args))}, false
	}
	in, out, cleanup := newPipedChild(t, tools, onCall)
	defer cleanup()

	c := New("fake", in, out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	res, err := c.CallTool(ctx, "echo", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, _ := res.Content[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, "ok:")
}
