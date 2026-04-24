package daemon

import (
	"context"
	"crypto/sha1"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
	"github.com/ayu5h-raj/mcp-gateway/internal/config"
	"github.com/ayu5h-raj/mcp-gateway/internal/configwrite"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/mcpchild"
	"github.com/ayu5h-raj/mcp-gateway/internal/pidfile"
	"github.com/ayu5h-raj/mcp-gateway/internal/secret"
	"github.com/ayu5h-raj/mcp-gateway/internal/supervisor"
	"github.com/ayu5h-raj/mcp-gateway/internal/tokens"
)

// unixMaxPath is the maximum AF_UNIX socket path length on most platforms
// (104 bytes on macOS including the null terminator; 108 on Linux).
// We use the stricter macOS limit to stay portable.
const unixMaxPath = 103

// ChooseSocketPath returns the UNIX socket path for home. If the natural
// path (home/sock) would exceed the platform AF_UNIX limit, a short
// /tmp-based path derived from a hash of home is used instead.
// Exported so CLI commands (start, stop, status) can compute the same path
// without duplicating the fallback logic.
func ChooseSocketPath(home string) string {
	natural := filepath.Join(home, "sock")
	if len(natural) <= unixMaxPath {
		return natural
	}
	// Fall back to a /tmp path keyed by the first 8 hex chars of SHA-1(home).
	h := sha1.Sum([]byte(home))
	return fmt.Sprintf("/tmp/mgw-%x.sock", h[:4])
}

// Daemon orchestrates the config watcher, supervisor, aggregator, and HTTP server.
type Daemon struct {
	Home    string
	Logger  *slog.Logger
	Version string

	mu             sync.Mutex
	cfg            *config.Config
	sup            *supervisor.Supervisor
	agg            *aggregator.Aggregator
	clients        map[string]*mcpchild.Client
	events         *event.Bus
	pidfileRelease func()
	socketPath     string
	startedAt      time.Time
	httpPort       int
}

// New returns a Daemon with the given home directory and optional logger.
func New(home string, logger *slog.Logger) *Daemon {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Daemon{
		Home:    home,
		Logger:  logger,
		Version: "0.2",
		clients: map[string]*mcpchild.Client{},
		events:  event.New(10000),
	}
}

// Run starts the daemon and blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	configPath := filepath.Join(d.Home, "config.jsonc")
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found at %s: %w", configPath, err)
	}

	// Pidfile first — refuse double-start.
	pidPath := filepath.Join(d.Home, "daemon.pid")
	release, existing, err := pidfile.Acquire(pidPath)
	if err != nil {
		return fmt.Errorf("daemon already running (pid=%d): %w", existing, err)
	}
	d.pidfileRelease = release
	defer func() {
		if d.pidfileRelease != nil {
			d.pidfileRelease()
		}
	}()

	watcher, err := config.NewWatcher(configPath)
	if err != nil {
		return fmt.Errorf("config watcher: %w", err)
	}
	defer watcher.Close()

	var initial *config.Config
	select {
	case initial = <-watcher.Changes():
	case err := <-watcher.Errors():
		return fmt.Errorf("initial config: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	logDir := filepath.Join(d.Home, "servers")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("log dir: %w", err)
	}

	d.startedAt = time.Now()
	d.httpPort = initial.Daemon.HTTPPort
	d.socketPath = ChooseSocketPath(d.Home)
	// Remove any stale socket file from a previous unclean shutdown.
	_ = os.Remove(d.socketPath)

	d.agg = aggregator.New()
	d.sup = supervisor.New(supervisor.SupervisorOpts{
		LogDir:             logDir,
		MaxRestartAttempts: initial.Daemon.ChildRestartMaxAttempts,
		BackoffMaxSeconds:  initial.Daemon.ChildRestartBackoffMaxSeconds,
		Hook: func(name string, prev, next supervisor.State, hookErr error) {
			ev := event.Event{
				Server: name,
				Extra:  map[string]any{"prev": prev.String(), "next": next.String()},
			}
			switch next {
			case supervisor.StateRunning:
				ev.Kind = event.KindChildAttached
			case supervisor.StateErrored, supervisor.StateRestarting:
				ev.Kind = event.KindChildCrashed
				if hookErr != nil {
					ev.Error = hookErr.Error()
				}
			case supervisor.StateDisabled:
				ev.Kind = event.KindChildDisabled
			default:
				return
			}
			d.events.Publish(ev)
		},
	})
	go d.sup.Run(ctx)
	go d.attachLoop(ctx)

	// Wire aggregator change callbacks → events.
	d.agg.OnToolsChanged(func() {
		d.events.Publish(event.Event{Kind: event.KindToolsChanged})
	})
	d.agg.OnResourcesChanged(func() {
		d.events.Publish(event.Event{Kind: event.KindResourcesChanged})
	})
	d.agg.OnPromptsChanged(func() {
		d.events.Publish(event.Event{Kind: event.KindPromptsChanged})
	})

	d.reconcile(ctx, initial)

	// TCP listener — only /mcp.
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", NewMCPHandler(d.agg, d.events))
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", initial.Daemon.HTTPPort)
	tcpLn, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", tcpAddr, err)
	}
	tcpSrv := &http.Server{Handler: mcpMux, ReadHeaderTimeout: 5 * time.Second}
	d.Logger.Info("mcp-gateway listening (tcp)", "addr", tcpAddr)
	go func() {
		if err := tcpSrv.Serve(tcpLn); err != nil && err != http.ErrServerClosed {
			d.Logger.Error("tcp serve", "err", err)
		}
	}()

	// UNIX socket listener — both /mcp and /admin/*.
	unixMux := http.NewServeMux()
	unixMux.Handle("/mcp", NewMCPHandler(d.agg, d.events))
	unixMux.Handle("/admin/", admin.NewHandler(d))
	unixLn, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", d.socketPath, err)
	}
	if err := os.Chmod(d.socketPath, 0o600); err != nil {
		_ = unixLn.Close()
		return fmt.Errorf("chmod sock: %w", err)
	}
	unixSrv := &http.Server{Handler: unixMux, ReadHeaderTimeout: 5 * time.Second}
	d.Logger.Info("mcp-gateway listening (unix)", "sock", d.socketPath)
	go func() {
		if err := unixSrv.Serve(unixLn); err != nil && err != http.ErrServerClosed {
			d.Logger.Error("unix serve", "err", err)
		}
	}()

	for {
		select {
		case cfg := <-watcher.Changes():
			d.Logger.Info("config changed, reconciling")
			d.events.Publish(event.Event{Kind: event.KindConfigReload})
			d.reconcile(ctx, cfg)
		case err := <-watcher.Errors():
			d.Logger.Error("config error", "err", err)
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tcpSrv.Shutdown(shutCtx)
			_ = unixSrv.Shutdown(shutCtx)
			cancel()
			_ = os.Remove(d.socketPath)
			return nil
		}
	}
}

// reconcile synchronizes supervisor + aggregator with the latest config.
func (d *Daemon) reconcile(ctx context.Context, cfg *config.Config) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg

	wanted := map[string]struct{}{}
	for name, s := range cfg.MCPServers {
		if !s.Enabled {
			continue
		}
		prefix := config.EffectivePrefix(name, s)
		wanted[prefix] = struct{}{}

		// Resolve ${env:NAME} references before passing env to the supervisor.
		resolvedEnv, err := secret.ResolveEnv(s.Env)
		if err != nil {
			d.Logger.Error("env resolution failed", "server", name, "err", err)
			continue
		}
		d.sup.Set(name, supervisor.ServerSpec{
			Name:    name,
			Command: s.Command,
			Args:    s.Args,
			Env:     resolvedEnv,
		})
		if _, ok := d.clients[prefix]; !ok {
			if p := d.sup.Process(name); p != nil {
				client := mcpchild.New(name, p.Stdin, p.Stdout)
				if err := client.Initialize(ctx); err != nil {
					d.Logger.Error("initialize child", "server", name, "err", err)
					continue
				}
				d.clients[prefix] = client
				d.agg.AddServer(prefix, client)
				if err := d.agg.RefreshAll(ctx); err != nil {
					d.Logger.Error("refresh", "err", err)
				}
			}
			// If process not yet up, attachLoop will retry.
		}
	}
	for prefix := range d.clients {
		if _, keep := wanted[prefix]; !keep {
			d.agg.RemoveServer(prefix)
			delete(d.clients, prefix)
			for name, s := range cfg.MCPServers {
				if config.EffectivePrefix(name, s) == prefix {
					d.sup.Remove(name)
				}
			}
		}
	}
}

// attachLoop re-attaches clients for servers that the supervisor has
// transitioned into StateRunning since the last reconcile.
func (d *Daemon) attachLoop(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.reattach(ctx)
		}
	}
}

func (d *Daemon) reattach(ctx context.Context) {
	d.mu.Lock()
	cfg := d.cfg
	if cfg == nil {
		d.mu.Unlock()
		return
	}
	sup := d.sup
	agg := d.agg
	type want struct{ name, prefix string }
	var toAttach []want
	for name, s := range cfg.MCPServers {
		if !s.Enabled {
			continue
		}
		prefix := config.EffectivePrefix(name, s)
		if _, has := d.clients[prefix]; has {
			continue
		}
		if sup.Status(name).State != supervisor.StateRunning {
			continue
		}
		toAttach = append(toAttach, want{name, prefix})
	}
	d.mu.Unlock()

	for _, w := range toAttach {
		p := sup.Process(w.name)
		if p == nil {
			continue
		}
		client := mcpchild.New(w.name, p.Stdin, p.Stdout)
		ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := client.Initialize(ictx); err != nil {
			cancel()
			d.Logger.Warn("attach: initialize failed", "server", w.name, "err", err)
			continue
		}
		cancel()

		d.mu.Lock()
		if _, exists := d.clients[w.prefix]; exists {
			d.mu.Unlock()
			continue
		}
		d.clients[w.prefix] = client
		d.mu.Unlock()
		agg.AddServer(w.prefix, client)
		if err := agg.RefreshAll(ctx); err != nil {
			d.Logger.Warn("attach: refresh failed", "server", w.name, "err", err)
		} else {
			d.Logger.Info("attached", "server", w.name, "prefix", w.prefix)
		}
	}
}

// --- admin.Daemon interface implementation ---

func (d *Daemon) Status() admin.Status {
	d.mu.Lock()
	numServers := len(d.clients)
	d.mu.Unlock()
	return admin.Status{
		PID:        os.Getpid(),
		StartedAt:  d.startedAt,
		HTTPPort:   d.httpPort,
		SocketPath: d.socketPath,
		Version:    d.Version,
		NumServers: numServers,
		NumTools:   len(d.agg.Tools()),
		ConfigPath: filepath.Join(d.Home, "config.jsonc"),
	}
}

func (d *Daemon) Servers() []admin.ServerInfo {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return nil
	}
	statuses := d.sup.List()
	statusByName := map[string]supervisor.Status{}
	for _, s := range statuses {
		statusByName[s.Name] = s
	}
	allTools := d.agg.Tools()
	est := tokens.CharBy4{}
	tokensByPrefix := map[string]int{}
	countByPrefix := map[string]int{}
	for _, t := range allTools {
		tokensByPrefix[t.Server] += tokens.ToolTokens(t, est)
		countByPrefix[t.Server]++
	}
	out := make([]admin.ServerInfo, 0, len(cfg.MCPServers))
	for name, s := range cfg.MCPServers {
		prefix := config.EffectivePrefix(name, s)
		st := statusByName[name]
		errStr := ""
		if st.LastError != nil {
			errStr = st.LastError.Error()
		}
		out = append(out, admin.ServerInfo{
			Name:         name,
			Prefix:       prefix,
			State:        st.State.String(),
			Enabled:      s.Enabled,
			RestartCount: st.RestartCount,
			StartedAt:    st.StartedAt,
			LastError:    errStr,
			LogPath:      filepath.Join(d.Home, "servers", name+".log"),
			ToolCount:    countByPrefix[prefix],
			EstTokens:    tokensByPrefix[prefix],
		})
	}
	return out
}

func (d *Daemon) Server(name string) (admin.ServerInfo, bool) {
	for _, s := range d.Servers() {
		if s.Name == name {
			return s, true
		}
	}
	return admin.ServerInfo{}, false
}

func (d *Daemon) Tools() []admin.ToolInfo {
	return admin.HelperToolsFromAggregator(d.agg.Tools())
}

func (d *Daemon) Bus() *event.Bus { return d.events }

func (d *Daemon) ConfigPath() string { return filepath.Join(d.Home, "config.jsonc") }

func (d *Daemon) ConfigBytes() ([]byte, error) {
	return os.ReadFile(d.ConfigPath())
}

func (d *Daemon) AddServer(spec admin.ServerSpec) error {
	return configwrite.Apply(d.ConfigPath(), func(c *config.Config) error {
		c.MCPServers[spec.Name] = config.Server{
			Command: spec.Command,
			Args:    spec.Args,
			Env:     spec.Env,
			Enabled: spec.Enabled,
			Prefix:  spec.Prefix,
		}
		return nil
	})
}

func (d *Daemon) RemoveServer(name string) error {
	return configwrite.Apply(d.ConfigPath(), func(c *config.Config) error {
		if _, ok := c.MCPServers[name]; !ok {
			return fmt.Errorf("no such server %q", name)
		}
		delete(c.MCPServers, name)
		return nil
	})
}

func (d *Daemon) EnableServer(name string) error  { return d.toggle(name, true) }
func (d *Daemon) DisableServer(name string) error { return d.toggle(name, false) }

func (d *Daemon) toggle(name string, enabled bool) error {
	return configwrite.Apply(d.ConfigPath(), func(c *config.Config) error {
		s, ok := c.MCPServers[name]
		if !ok {
			return fmt.Errorf("no such server %q", name)
		}
		s.Enabled = enabled
		c.MCPServers[name] = s
		return nil
	})
}

func (d *Daemon) Reload() error {
	// Touch the config file so fsnotify picks up the change.
	now := time.Now()
	return os.Chtimes(d.ConfigPath(), now, now)
}
