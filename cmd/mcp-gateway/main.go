package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
	"github.com/ayu5h-raj/mcp-gateway/internal/bridge"
	"github.com/ayu5h-raj/mcp-gateway/internal/daemon"
)

// version is set at build time via -ldflags.
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mcp-gateway",
		Short:         "Local-first MCP aggregator daemon",
		Long:          "mcp-gateway aggregates multiple MCP servers behind one endpoint.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newStdioCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newAddCmd())
	root.AddCommand(newRmCmd())
	root.AddCommand(newEnableCmd())
	root.AddCommand(newDisableCmd())
	root.AddCommand(newSecretCmd())
	root.AddCommand(newTUICmd())
	root.AddCommand(newServiceCmd())
	return root
}

func newDaemonCmd() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the mcp-gateway daemon (long-running)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if home == "" {
				h, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				home = filepath.Join(h, ".mcp-gateway")
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			d := daemon.New(home, logger)

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				cancel()
			}()
			return d.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&home, "home", "", "path to ~/.mcp-gateway (default: $HOME/.mcp-gateway)")
	return cmd
}

func newStdioCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "stdio",
		Short: "Run as a stdio bridge to the local daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
			return bridge.Run(cmd.Context(), bridge.RunConfig{
				URL:    url,
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
			})
		},
	}
	cmd.Flags().IntVar(&port, "port", 7823, "daemon HTTP port")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print daemon status (via /admin/status over UNIX socket)",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			daemonHome := filepath.Join(home, ".mcp-gateway")
			sock := daemon.ChooseSocketPath(daemonHome)
			if _, err := os.Stat(sock); err != nil {
				return fmt.Errorf("daemon not running (no socket at %s)", sock)
			}
			c := adminclient.New(sock)
			var st admin.Status
			if err := c.Get("/admin/status", &st); err != nil {
				return err
			}
			fmt.Printf("daemon: OK (pid=%d, port=%d, version=%s, started=%s)\n",
				st.PID, st.HTTPPort, st.Version, st.StartedAt.Format(time.RFC3339))
			fmt.Printf("  servers: %d, tools: %d\n", st.NumServers, st.NumTools)
			fmt.Printf("  config:  %s\n", st.ConfigPath)
			fmt.Printf("  socket:  %s\n", st.SocketPath)
			return nil
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
