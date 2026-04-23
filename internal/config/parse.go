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
	// Default values first, then overlay what the user set.
	c := &Config{
		Version:    Version,
		Daemon:     DefaultDaemon(),
		McpServers: map[string]Server{},
	}
	dec := json.NewDecoder(bytes.NewReader(pure))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, &FormatError{Err: fmt.Errorf("decode: %w", err)}
	}
	// Reapply daemon defaults for any zero-valued fields the user omitted
	// (json.Decode overwrites our defaults with zero when fields are absent
	// in a subobject).
	d := c.Daemon
	def := DefaultDaemon()
	if d.HTTPPort == 0 {
		d.HTTPPort = def.HTTPPort
	}
	if d.LogLevel == "" {
		d.LogLevel = def.LogLevel
	}
	if d.EventBufferSize == 0 {
		d.EventBufferSize = def.EventBufferSize
	}
	if d.ChildRestartBackoffMaxSeconds == 0 {
		d.ChildRestartBackoffMaxSeconds = def.ChildRestartBackoffMaxSeconds
	}
	if d.ChildRestartMaxAttempts == 0 {
		d.ChildRestartMaxAttempts = def.ChildRestartMaxAttempts
	}
	c.Daemon = d
	return c, nil
}

// ParseFile is a convenience wrapper around Parse that opens a file by path.
func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &FormatError{Path: path, Err: err}
	}
	defer f.Close()
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
