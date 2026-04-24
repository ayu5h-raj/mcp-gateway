// Package tokens provides token-cost estimation for MCP tool definitions.
//
// v0.2 uses a chars/4 heuristic — clearly approximate, zero deps. The interface
// allows swapping in a real tokenizer later without touching consumers.
package tokens

import "github.com/ayu5h-raj/mcp-gateway/internal/aggregator"

// Estimator returns an approximate token count for a string.
type Estimator interface {
	Tokens(text string) int
}

// CharBy4 implements Estimator using the chars/4 heuristic. Good enough to
// answer "which server is eating my context budget?" — which is the only
// question the user actually asks.
type CharBy4 struct{}

// Tokens returns len(text)/4.
func (CharBy4) Tokens(text string) int { return len(text) / 4 }

// ToolTokens estimates the token cost of a tool definition: name + description
// + raw input schema bytes.
func ToolTokens(t aggregator.Tool, e Estimator) int {
	return e.Tokens(t.Name) + e.Tokens(t.Description) + e.Tokens(string(t.InputSchema))
}
