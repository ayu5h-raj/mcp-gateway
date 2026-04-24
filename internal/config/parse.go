package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tidwall/jsonc"
)

// Parse reads config bytes (JSONC) from r and returns a fully-defaulted Config.
// Errors are wrapped in FormatError (the caller may Unwrap for the raw cause).
func Parse(r io.Reader) (*Config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, &FormatError{Err: fmt.Errorf("read: %w", err)}
	}
	// Strip JSONC comments and trailing commas → canonical JSON.
	pure := jsonc.ToJSON(raw)
	// Pre-populate with defaults. encoding/json only overwrites fields that
	// appear in the input; absent fields keep their pre-Decode values, so
	// omitting a field in config yields its default. Explicit zero values
	// (e.g. child_restart_max_attempts: 0 meaning "never retry") are
	// preserved verbatim — validate.go enforces allowed ranges.
	c := &Config{
		Version:    Version,
		Daemon:     DefaultDaemon(),
		MCPServers: map[string]Server{},
	}
	dec := json.NewDecoder(bytes.NewReader(pure))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, &FormatError{Err: fmt.Errorf("decode: %w", err)}
	}
	return c, nil
}

// ParseFile is a convenience wrapper around Parse that opens a file by path.
func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &FormatError{Path: path, Err: err}
	}
	defer func() { _ = f.Close() }()
	c, err := Parse(f)
	if err != nil {
		if fe, ok := err.(*FormatError); ok {
			fe.Path = path
			return nil, fe
		}
		return nil, &FormatError{Path: path, Err: err}
	}
	return c, nil
}
