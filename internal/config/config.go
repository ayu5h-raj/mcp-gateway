package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Version is the currently-supported schema version.
const Version = 1

// Config is the top-level user-facing configuration.
type Config struct {
	Schema     string            `json:"$schema,omitempty"`
	Version    int               `json:"version"`
	Daemon     Daemon            `json:"daemon"`
	McpServers map[string]Server `json:"mcpServers"`
}

// Daemon groups daemon-scoped settings.
type Daemon struct {
	HTTPPort                      int    `json:"http_port"`
	LogLevel                      string `json:"log_level"`
	EventBufferSize               int    `json:"event_buffer_size,omitempty"`
	ChildRestartBackoffMaxSeconds int    `json:"child_restart_backoff_max_seconds,omitempty"`
	ChildRestartMaxAttempts       int    `json:"child_restart_max_attempts,omitempty"`
}

// Server is one downstream MCP server definition.
type Server struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	Prefix  string            `json:"prefix,omitempty"`
}

// DefaultDaemon returns the daemon defaults applied when config omits fields.
func DefaultDaemon() Daemon {
	return Daemon{
		HTTPPort:                      7823,
		LogLevel:                      "info",
		EventBufferSize:               10000,
		ChildRestartBackoffMaxSeconds: 60,
		ChildRestartMaxAttempts:       5,
	}
}

// EffectivePrefix returns the tool/resource prefix for a server.
// Defaults to the server key (map name) if unset.
func EffectivePrefix(name string, s Server) string {
	if strings.TrimSpace(s.Prefix) != "" {
		return s.Prefix
	}
	return name
}

// DefaultConfigPath returns the default on-disk location for the user's config.
func DefaultConfigPath(home string) string {
	return filepath.Join(home, ".mcp-gateway", "config.jsonc")
}

// FormatError wraps a parse/validate error with the originating file.
type FormatError struct {
	Path string
	Err  error
}

func (e *FormatError) Error() string {
	if e.Path == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Err.Error())
}

func (e *FormatError) Unwrap() error { return e.Err }
