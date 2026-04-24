package mcpchild

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/testutil/fakechild"
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

// TestClient_CallbackRace verifies no -race flag trips when callbacks are
// registered shortly before/while notifications are being dispatched.
func TestClient_CallbackRace(t *testing.T) {
	tools := []fakechild.Tool{{Name: "t", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := fakechild.New(tools, nil)
	go func() {
		_ = srv.Serve(inR, outW)
		_ = outW.Close()
	}()
	t.Cleanup(func() { _ = inW.Close() })

	c := New("x", inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	// Concurrently register callbacks from multiple goroutines.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.OnToolsListChanged(func() {})
			c.OnResourcesListChanged(func() {})
			c.OnPromptsListChanged(func() {})
			c.OnResourceUpdated(func(string) {})
		}()
	}
	wg.Wait()
}

// TestClient_ConcurrentRequests verifies concurrent calls don't interleave
// JSON-RPC frames on the wire.
func TestClient_ConcurrentRequests(t *testing.T) {
	tools := []fakechild.Tool{{Name: "t", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})}}
	in, out, cleanup := newPipedChild(t, tools, nil)
	defer cleanup()

	c := New("x", in, out)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	const N = 40
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			_, err := c.ListTools(ctx)
			errs <- err
		}()
	}
	for i := 0; i < N; i++ {
		select {
		case err := <-errs:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("concurrent ListTools timed out")
		}
	}
}

// TestClient_ChildExitUnblocksInflight verifies that inflight requests fail
// fast (rather than block forever) when the child process exits.
//
// We do NOT use fakechild here: fakechild responds to tools/list synchronously,
// so the request would complete before outR.Close() is called and the drain
// path would never be exercised. Instead we hand-roll a minimal child that
// answers initialize and then silently consumes further frames without
// replying — guaranteeing that ListTools is still blocked in its select when
// we close the output pipe.
func TestClient_ChildExitUnblocksInflight(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	// Minimal child: answers initialize then swallows all subsequent frames.
	go func() {
		br := bufio.NewReader(inR)
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(line, &req)
		resp := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{}}`+"\n", req.ID))
		_, _ = outW.Write(resp)
		// Drain input without responding — keeps ListTools waiting.
		for {
			if _, err := br.ReadBytes('\n'); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })

	c := New("x", inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	// Fire a ListTools; while it's pending, simulate the child dying by
	// closing the read end from outside.
	done := make(chan error, 1)
	go func() {
		_, err := c.ListTools(context.Background()) // non-cancelling!
		done <- err
	}()
	// Give the call time to enter the select.
	time.Sleep(100 * time.Millisecond)
	_ = outR.Close() // simulate child process exit: readLoop hits EOF

	select {
	case err := <-done:
		require.Error(t, err, "inflight request must error when child exits")
	case <-time.After(2 * time.Second):
		t.Fatal("inflight request was not unblocked after child exit")
	}
}
