// Package admin serves the /admin/* HTTP surface used by the TUI (Plan 03)
// and mutation CLI subcommands. It is consumed only over the UNIX socket
// (file mode 0600) — never exposed on TCP. See daemon.Run.
package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/secret"
	"github.com/ayu5h-raj/mcp-gateway/internal/supervisor"
	"github.com/ayu5h-raj/mcp-gateway/internal/tokens"
)

// Daemon is the surface admin handlers need. The real *daemon.Daemon
// implements it; tests use mocks.
type Daemon interface {
	Status() Status
	Servers() []ServerInfo
	Server(name string) (ServerInfo, bool)
	Tools() []ToolInfo
	Bus() *event.Bus
	ConfigPath() string
	ConfigBytes() ([]byte, error)

	// Mutations — used by Phase 8.
	AddServer(spec ServerSpec) error
	RemoveServer(name string) error
	EnableServer(name string) error
	DisableServer(name string) error
	Reload() error
}

// Status is the daemon-level snapshot.
type Status struct {
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
	HTTPPort   int       `json:"http_port"`
	SocketPath string    `json:"socket_path"`
	Version    string    `json:"version"`
	NumServers int       `json:"num_servers"`
	NumTools   int       `json:"num_tools"`
	ConfigPath string    `json:"config_path"`
}

// ServerInfo is the per-server view.
type ServerInfo struct {
	Name         string           `json:"name"`
	Prefix       string           `json:"prefix"`
	State        string           `json:"state"`
	Enabled      bool             `json:"enabled"`
	RestartCount int              `json:"restart_count"`
	StartedAt    time.Time        `json:"started_at,omitempty"`
	LastError    string           `json:"last_error,omitempty"`
	LogPath      string           `json:"log_path"`
	ToolCount    int              `json:"tool_count"`
	EstTokens    int              `json:"est_tokens"`
	Status       supervisor.State `json:"-"` // for internal use
}

// ToolInfo is the per-tool view.
type ToolInfo struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	EstTokens   int             `json:"est_tokens"`
}

// ServerSpec is the body of POST /admin/server.
type ServerSpec struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Prefix  string            `json:"prefix,omitempty"`
	Enabled bool              `json:"enabled"`
}

// SecretInfo is one entry in GET /admin/secret.
type SecretInfo struct {
	Name   string   `json:"name"`
	UsedBy []string `json:"used_by"`
	Set    bool     `json:"set"`
}

// NewHandler returns the admin /admin/* http.Handler.
func NewHandler(d Daemon) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, d.Status())
	})
	mux.HandleFunc("/admin/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, d.Servers())
		case http.MethodPost:
			handleAddServer(d, w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/admin/servers/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin/servers/")
		// /admin/servers/{name}, /admin/servers/{name}/enable, /admin/servers/{name}/disable
		switch {
		case strings.HasSuffix(path, "/enable") && r.Method == http.MethodPost:
			handleEnableServer(d, w, strings.TrimSuffix(path, "/enable"))
		case strings.HasSuffix(path, "/disable") && r.Method == http.MethodPost:
			handleDisableServer(d, w, strings.TrimSuffix(path, "/disable"))
		case r.Method == http.MethodGet:
			si, ok := d.Server(path)
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, si)
		case r.Method == http.MethodDelete:
			handleRemoveServer(d, w, path)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/admin/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, d.Tools())
	})
	mux.HandleFunc("/admin/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		serveSSE(d.Bus(), w, r)
	})
	mux.HandleFunc("/admin/secret", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleListSecrets(d, w, r)
	})
	// /admin/secret/* — POST/DELETE removed in revised plan (no keychain in v0.2).
	mux.HandleFunc("/admin/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		b, err := d.ConfigBytes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/admin/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := d.Reload(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func handleAddServer(d Daemon, w http.ResponseWriter, r *http.Request) {
	var spec ServerSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := d.AddServer(spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func handleRemoveServer(d Daemon, w http.ResponseWriter, name string) {
	if err := d.RemoveServer(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleEnableServer(d Daemon, w http.ResponseWriter, name string) {
	if err := d.EnableServer(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleDisableServer(d Daemon, w http.ResponseWriter, name string) {
	if err := d.DisableServer(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleListSecrets(d Daemon, w http.ResponseWriter, _ *http.Request) {
	// Walk the config to find ${env:NAME} references and report whether
	// each referenced var is currently set in the daemon's process env.
	cfgBytes, err := d.ConfigBytes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	names := extractEnvRefs(cfgBytes)
	out := make([]SecretInfo, 0, len(names))
	for name, used := range names {
		_, isSet := os.LookupEnv(name)
		out = append(out, SecretInfo{Name: name, UsedBy: used, Set: isSet})
	}
	writeJSON(w, out)
}

// extractEnvRefs returns a map of env var name → server names that reference
// it via ${env:NAME} in their config Env map. Pure.
func extractEnvRefs(cfgBytes []byte) map[string][]string {
	out := map[string][]string{}
	var raw struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfgBytes, &raw); err != nil {
		return out
	}
	for srv, s := range raw.MCPServers {
		for _, v := range s.Env {
			for _, name := range secret.Refs(v) {
				out[name] = appendUnique(out[name], srv)
			}
		}
	}
	return out
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// HelperToolsFromAggregator builds []ToolInfo from an aggregator snapshot
// using the chars/4 estimator. Exposed for the daemon.
func HelperToolsFromAggregator(snapshot []aggregator.Tool) []ToolInfo {
	est := tokens.CharBy4{}
	out := make([]ToolInfo, 0, len(snapshot))
	for _, t := range snapshot {
		out = append(out, ToolInfo{
			Server:      t.Server,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(t.InputSchema),
			EstTokens:   tokens.ToolTokens(t, est),
		})
	}
	return out
}
