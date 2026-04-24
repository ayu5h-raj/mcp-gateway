package tokens

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
)

func TestCharBy4_Tokens(t *testing.T) {
	e := CharBy4{}
	assert.Equal(t, 0, e.Tokens(""))
	assert.Equal(t, 1, e.Tokens("abcd"))
	assert.Equal(t, 2, e.Tokens("abcdefgh"))
	assert.Equal(t, 2, e.Tokens("abcdefghi")) // 9/4=2
}

func TestToolTokens_SumsNameDescriptionAndSchema(t *testing.T) {
	e := CharBy4{}
	tool := aggregator.Tool{
		Name:        "abcd",                      // 1
		Description: "abcdefgh",                  // 2
		InputSchema: []byte(`{"type":"object"}`), // 17/4=4
	}
	got := ToolTokens(tool, e)
	assert.Equal(t, 1+2+4, got)
}
