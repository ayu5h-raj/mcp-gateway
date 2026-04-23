package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrefix_RoundTrip(t *testing.T) {
	p := "github"
	prefixed := PrefixTool(p, "create_issue")
	assert.Equal(t, "github__create_issue", prefixed)
	server, original, ok := ParsePrefixed(prefixed)
	assert.True(t, ok)
	assert.Equal(t, "github", server)
	assert.Equal(t, "create_issue", original)
}

func TestPrefix_NameWithInternalDoubleUnderscore(t *testing.T) {
	// "weird__tool" as original name should still parse correctly.
	prefixed := PrefixTool("ns", "weird__tool")
	assert.Equal(t, "ns__weird__tool", prefixed)
	server, original, ok := ParsePrefixed(prefixed)
	assert.True(t, ok)
	assert.Equal(t, "ns", server)
	assert.Equal(t, "weird__tool", original)
}

func TestPrefix_RejectsUnprefixed(t *testing.T) {
	_, _, ok := ParsePrefixed("no_prefix_here")
	assert.False(t, ok)
}

func TestPrefix_Resource(t *testing.T) {
	// scheme preserved; prefix added as first path segment.
	assert.Equal(t, "github+mcp://repos/foo", PrefixResourceURI("github", "mcp://repos/foo"))
	// opaque (no ://) URIs get a bare prefix.
	assert.Equal(t, "github__note-1", PrefixResourceURI("github", "note-1"))
	server, orig, ok := ParsePrefixedResourceURI("github+mcp://repos/foo")
	assert.True(t, ok)
	assert.Equal(t, "github", server)
	assert.Equal(t, "mcp://repos/foo", orig)
	server, orig, ok = ParsePrefixedResourceURI("github__note-1")
	assert.True(t, ok)
	assert.Equal(t, "github", server)
	assert.Equal(t, "note-1", orig)
}
