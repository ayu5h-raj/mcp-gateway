package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_MinimalValid(t *testing.T) {
	in := `{
	  "version": 1,
	  "daemon": { "http_port": 7823, "log_level": "info" },
	  "mcpServers": {
	    "github": {
	      "command": "npx",
	      "args": ["-y", "@modelcontextprotocol/server-github"],
	      "enabled": true
	    }
	  }
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, 1, c.Version)
	assert.Equal(t, 7823, c.Daemon.HTTPPort)
	assert.Equal(t, "info", c.Daemon.LogLevel)
	require.Contains(t, c.MCPServers, "github")
	gh := c.MCPServers["github"]
	assert.Equal(t, "npx", gh.Command)
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-github"}, gh.Args)
	assert.True(t, gh.Enabled)
}

func TestParse_StripsLineAndBlockComments(t *testing.T) {
	in := `{
	  // top-level comment
	  "version": 1, /* inline block */
	  "daemon": { "http_port": 7823, "log_level": "info" },
	  "mcpServers": {
	    // no servers configured yet
	  }
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, 1, c.Version)
	assert.Empty(t, c.MCPServers)
}

func TestParse_TolerantOfTrailingCommas(t *testing.T) {
	in := `{
	  "version": 1,
	  "daemon": { "http_port": 7823, "log_level": "info", },
	  "mcpServers": { "fs": { "command": "cat", "enabled": true, }, }
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.True(t, c.MCPServers["fs"].Enabled)
}

func TestParse_AppliesDefaults(t *testing.T) {
	in := `{
	  "version": 1,
	  "daemon": {},
	  "mcpServers": {}
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, 7823, c.Daemon.HTTPPort)
	assert.Equal(t, "info", c.Daemon.LogLevel)
	assert.Equal(t, 10000, c.Daemon.EventBufferSize)
	assert.Equal(t, 60, c.Daemon.ChildRestartBackoffMaxSeconds)
	assert.Equal(t, 5, c.Daemon.ChildRestartMaxAttempts)
}

func TestParse_ReturnsErrorOnMalformedJSON(t *testing.T) {
	in := `{"version": 1, "daemon": { "http_port": 7823,`
	_, err := Parse(strings.NewReader(in))
	require.Error(t, err)
}
