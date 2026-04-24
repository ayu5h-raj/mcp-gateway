package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
	"github.com/ayu5h-raj/mcp-gateway/internal/config"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/mcpchild"
	"github.com/ayu5h-raj/mcp-gateway/internal/supervisor"
)

// Daemon orchestrates the config watcher, supervisor, aggregator, and HTTP server.
type Daemon struct {
	Home   string
	Logger *slog.Logger

	mu      sync.Mutex
	cfg     *config.Config
	sup     *supervisor.Supervisor
	agg     *aggregator.Aggregator
	clients map[string]*mcpchild.Client
	events  *event.Bus
}

// New returns a Daemon with the given home directory and optional logger.
func New(home string, logger *slog.Logger) *Daemon {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Daemon{
		Home:    home,
		Logger:  logger,
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

	d.agg = aggregator.New()
	d.sup = supervisor.New(supervisor.SupervisorOpts{
		LogDir:             logDir,
		MaxRestartAttempts: initial.Daemon.ChildRestartMaxAttempts,
		BackoffMaxSeconds:  initial.Daemon.ChildRestartBackoffMaxSeconds,
	})
	go d.sup.Run(ctx)
	go d.attachLoop(ctx)

	d.reconcile(ctx, initial)

	mux := http.NewServeMux()
	mux.Handle("/mcp", NewMCPHandler(d.agg, d.events))
	addr := fmt.Sprintf("127.0.0.1:%d", initial.Daemon.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	d.Logger.Info("mcp-gateway listening", "addr", addr)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.Logger.Error("http serve", "err", err)
		}
	}()

	for {
		select {
		case cfg := <-watcher.Changes():
			d.Logger.Info("config changed, reconciling")
			d.reconcile(ctx, cfg)
		case err := <-watcher.Errors():
			d.Logger.Error("config error", "err", err)
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shutCtx)
			cancel()
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
		d.sup.Set(name, supervisor.ServerSpec{
			Name:    name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
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
