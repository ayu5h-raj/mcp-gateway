package aggregator

import "strings"

// Sep is the double-underscore separator used for tool/prompt names.
const Sep = "__"

// PrefixTool joins a server prefix and a tool name with "__".
func PrefixTool(prefix, name string) string { return prefix + Sep + name }

// ParsePrefixed splits a prefixed name back into (prefix, original, ok).
// Uses the FIRST "__" as the boundary so internal "__" in tool names is preserved.
func ParsePrefixed(prefixed string) (string, string, bool) {
	i := strings.Index(prefixed, Sep)
	if i <= 0 || i+len(Sep) >= len(prefixed) {
		return "", "", false
	}
	return prefixed[:i], prefixed[i+len(Sep):], true
}

// PrefixResourceURI returns a prefixed URI. For schemed URIs like "mcp://..."
// we produce "<prefix>+mcp://..." so clients can still see the scheme. For
// opaque URIs (no "://") we use the same "__" scheme as tools.
func PrefixResourceURI(prefix, uri string) string {
	if idx := strings.Index(uri, "://"); idx > 0 {
		return prefix + "+" + uri
	}
	return prefix + Sep + uri
}

// ParsePrefixedResourceURI is the inverse of PrefixResourceURI.
func ParsePrefixedResourceURI(uri string) (string, string, bool) {
	// Try the "<prefix>+<scheme>://..." form first.
	if plus := strings.Index(uri, "+"); plus > 0 {
		colonSlash := strings.Index(uri, "://")
		if colonSlash > plus {
			return uri[:plus], uri[plus+1:], true
		}
	}
	// Fall back to "<prefix>__<rest>".
	return ParsePrefixed(uri)
}
