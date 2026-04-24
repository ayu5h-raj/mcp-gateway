package main

import (
	"encoding/json"
	"os"

	"github.com/ayushraj/mcp-gateway/internal/testutil/fakechild"
)

func main() {
	tools := []fakechild.Tool{
		{Name: "ping", Description: "returns pong", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	}
	srv := fakechild.New(tools, func(_ string, _ json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("pong")}, false
	})
	_ = srv.Serve(os.Stdin, os.Stdout)
}
