// Package secret resolves ${env:NAME} placeholders in config values against
// the OS environment. v0.2 supports only the "env" scheme; the parser is
// scheme-aware so future plans can add e.g. "keychain" without breaking
// existing configs.
//
// "$$" escapes a literal "$". Missing env vars are hard errors (not silent
// empty substitution).
package secret

import (
	"fmt"
	"os"
	"strings"
)

// Resolve replaces every ${env:NAME} in s with os.Getenv(NAME).
// "$$" → literal "$". Errors loudly on missing env, unknown scheme, or
// malformed placeholder.
func Resolve(s string) (string, error) {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		// "$$" → literal "$"
		if i+1 < len(s) && s[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		if i+1 >= len(s) || s[i+1] != '{' {
			return "", fmt.Errorf("secret: stray $ at position %d in %q", i, s)
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return "", fmt.Errorf("secret: unterminated ${ at position %d in %q", i, s)
		}
		body := s[i+2 : i+2+end]
		colon := strings.IndexByte(body, ':')
		if colon <= 0 {
			return "", fmt.Errorf("secret: invalid placeholder %q (need scheme:name)", body)
		}
		scheme, name := body[:colon], body[colon+1:]
		if scheme != "env" {
			return "", fmt.Errorf("secret: unknown scheme %q (only \"env\" is supported in v0.2)", scheme)
		}
		v, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("secret: env var %q is not set", name)
		}
		b.WriteString(v)
		i += 2 + end + 1
	}
	return b.String(), nil
}

// ResolveEnv applies Resolve to every value in env. On any error returns
// the error wrapped with the originating env-map key.
func ResolveEnv(env map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(env))
	for k, v := range env {
		resolved, err := Resolve(v)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// Refs returns the unique env var names referenced via ${env:NAME} in s.
// Order is undefined. Unknown schemes are ignored (not errored).
func Refs(s string) []string {
	seen := map[string]struct{}{}
	var out []string
	i := 0
	for i < len(s) {
		j := strings.Index(s[i:], "${env:")
		if j < 0 {
			break
		}
		start := i + j + len("${env:")
		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			break
		}
		name := s[start : start+end]
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			out = append(out, name)
		}
		i = start + end + 1
	}
	return out
}
