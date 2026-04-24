package aggregator

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/mcpchild"
	"github.com/ayu5h-raj/mcp-gateway/internal/testutil/fakechild"
)

func newClient(t *testing.T, name string, tools []fakechild.Tool) (*mcpchild.Client, func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := fakechild.New(tools, func(n string, args json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("called:" + name + "/" + n)}, false
	})
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(inR, outW)
		outW.Close()
		close(done)
	}()
	c := mcpchild.New(name, inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))
	cleanup := func() {
		inW.Close()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
	return c, cleanup
}

func TestAggregator_MergesToolsFromTwoChildren(t *testing.T) {
	a := New()
	clientA, cleanA := newClient(t, "a", []fakechild.Tool{
		{Name: "foo", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanA()
	clientB, cleanB := newClient(t, "b", []fakechild.Tool{
		{Name: "bar", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanB()

	a.AddServer("alpha", clientA)
	a.AddServer("beta", clientB)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, a.RefreshAll(ctx))

	tools := a.Tools()
	require.Len(t, tools, 2)
	names := []string{tools[0].Name, tools[1].Name}
	assert.ElementsMatch(t, []string{"alpha__foo", "beta__bar"}, names)
}

func TestAggregator_RoutesToolCall(t *testing.T) {
	a := New()
	clientA, cleanA := newClient(t, "a", []fakechild.Tool{
		{Name: "say", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanA()
	a.AddServer("alpha", clientA)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, a.RefreshAll(ctx))

	res, err := a.CallTool(ctx, "alpha__say", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	content := res.Content[0].(map[string]any)
	assert.Contains(t, content["text"].(string), "called:a/say")
}

func TestAggregator_EmitsListChangedOnServerRemoved(t *testing.T) {
	a := New()
	clientA, cleanA := newClient(t, "a", []fakechild.Tool{
		{Name: "foo", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanA()
	a.AddServer("alpha", clientA)

	var ticks atomic.Int32
	a.OnToolsChanged(func() { ticks.Add(1) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, a.RefreshAll(ctx))
	assert.GreaterOrEqual(t, int(ticks.Load()), 1)

	before := ticks.Load()
	a.RemoveServer("alpha")
	assert.Greater(t, ticks.Load(), before)
	assert.Empty(t, a.Tools())
}
