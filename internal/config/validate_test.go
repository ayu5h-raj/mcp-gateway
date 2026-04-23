package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCfg() *Config {
	return &Config{
		Version: 1,
		Daemon:  DefaultDaemon(),
		McpServers: map[string]Server{
			"ok": {Command: "echo", Enabled: true},
		},
	}
}

func TestValidate_Minimal(t *testing.T) {
	require.NoError(t, Validate(validCfg()))
}

func TestValidate_RejectsUnsupportedVersion(t *testing.T) {
	c := validCfg()
	c.Version = 2
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestValidate_RejectsInvalidPort(t *testing.T) {
	c := validCfg()
	c.Daemon.HTTPPort = 0
	require.Error(t, Validate(c))
	c.Daemon.HTTPPort = 70000
	require.Error(t, Validate(c))
}

func TestValidate_RejectsEmptyCommand(t *testing.T) {
	c := validCfg()
	c.McpServers["bad"] = Server{Command: "", Enabled: true}
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad")
	assert.Contains(t, err.Error(), "command")
}

func TestValidate_RejectsEmptyPrefixWhenExplicit(t *testing.T) {
	c := validCfg()
	s := c.McpServers["ok"]
	// Explicit empty prefix is NOT allowed (collision footgun).
	s.Prefix = "  "
	c.McpServers["ok"] = s
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefix")
}

func TestValidate_RejectsDuplicatePrefix(t *testing.T) {
	c := &Config{
		Version: 1,
		Daemon:  DefaultDaemon(),
		McpServers: map[string]Server{
			"a": {Command: "x", Enabled: true, Prefix: "dup"},
			"b": {Command: "x", Enabled: true, Prefix: "dup"},
		},
	}
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate prefix")
}

func TestValidate_RejectsBadServerName(t *testing.T) {
	c := validCfg()
	c.McpServers["has space"] = Server{Command: "echo", Enabled: true}
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestValidate_RejectsBadLogLevel(t *testing.T) {
	c := validCfg()
	c.Daemon.LogLevel = "chatty"
	require.Error(t, Validate(c))
}
