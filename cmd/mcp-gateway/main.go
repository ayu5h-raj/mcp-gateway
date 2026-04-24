package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ayushraj/mcp-gateway/internal/bridge"
	"github.com/ayushraj/mcp-gateway/internal/daemon"
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
		Short: "Print daemon status",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("status: not yet implemented")
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
