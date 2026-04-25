// Package clientcfg detects and rewrites the MCP server lists in well-known
// client configs (Claude Desktop, Cursor) without disturbing other keys.
package clientcfg

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Client describes one supported MCP client and where to find its config.
type Client struct {
	Name       string // human-readable: "Claude Desktop", "Cursor"
	ID         string // stable ID: "claude-desktop", "cursor"
	ConfigPath string // absolute path on this machine
}

// Server is one downstream MCP server entry from a client's config.
type Server struct {
	Name    string            `json:"-"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"-"` // not in client schema; default true
}

// Detected groups one client with its parsed server list (or a parse error).
type Detected struct {
	Client  Client
	Servers []Server
	Err     error // non-nil if the file existed but could not be read or parsed
}

// ErrConfigMissing means the client's config file does not exist on disk.
// Callers should treat this as "client not installed" and skip it silently.
var ErrConfigMissing = errors.New("client config missing")

// KnownClients returns the list of clients we know how to read on this OS.
func KnownClients() []Client {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return []Client{
			{
				Name:       "Claude Desktop",
				ID:         "claude-desktop",
				ConfigPath: filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
			},
			{
				Name:       "Cursor",
				ID:         "cursor",
				ConfigPath: filepath.Join(home, ".cursor", "mcp.json"),
			},
		}
	case "linux":
		// Claude Desktop on Linux uses XDG; Cursor uses ~/.cursor.
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		return []Client{
			{
				Name:       "Claude Desktop",
				ID:         "claude-desktop",
				ConfigPath: filepath.Join(xdg, "Claude", "claude_desktop_config.json"),
			},
			{
				Name:       "Cursor",
				ID:         "cursor",
				ConfigPath: filepath.Join(home, ".cursor", "mcp.json"),
			},
		}
	}
	return nil
}

// Detect reads each known client's config and returns one Detected per
// client whose file exists. Missing files are skipped (no entry returned);
// parse errors return an entry with Err set and Servers nil.
func Detect() []Detected {
	var out []Detected
	for _, c := range KnownClients() {
		servers, err := readClient(c)
		if errors.Is(err, ErrConfigMissing) {
			continue
		}
		out = append(out, Detected{Client: c, Servers: servers, Err: err})
	}
	return out
}

// readClient is dispatched to the per-client reader. Defined here so Detect
// can stay generic; per-client files implement the actual format.
func readClient(c Client) ([]Server, error) {
	switch c.ID {
	case "claude-desktop":
		return readClaudeDesktop(c.ConfigPath)
	case "cursor":
		return readCursor(c.ConfigPath)
	default:
		return nil, errors.New("clientcfg: unknown client id: " + c.ID)
	}
}

// Patch rewrites the named client's config to remove removedServers and
// install a single "mcp-gateway" stdio entry pointing at gatewayBinary.
// The original file is backed up to <path>.bak.<timestamp>; the rewrite
// itself is atomic (tmp + rename). Preserves all unknown top-level keys.
func Patch(c Client, removedServers []string, gatewayBinary string) error {
	switch c.ID {
	case "claude-desktop":
		return patchClaudeDesktop(c.ConfigPath, removedServers, gatewayBinary)
	case "cursor":
		return patchCursor(c.ConfigPath, removedServers, gatewayBinary)
	default:
		return errors.New("clientcfg: unknown client id: " + c.ID)
	}
}
