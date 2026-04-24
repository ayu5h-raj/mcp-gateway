package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

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
	root.AddCommand(newListCmd())
	root.AddCommand(newAddCmd())
	root.AddCommand(newRmCmd())
	root.AddCommand(newEnableCmd())
	root.AddCommand(newDisableCmd())
	root.AddCommand(newSecretCmd())
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
	var port int
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print daemon status (hits /mcp initialize)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
			req := []byte(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"status","version":"0"}}}`)
			r, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, url, bytes.NewReader(req))
			if err != nil {
				return err
			}
			r.Header.Set("Content-Type", "application/json")
			cli := &http.Client{Timeout: 2 * time.Second}
			resp, err := cli.Do(r)
			if err != nil {
				return fmt.Errorf("daemon unreachable at %s: %w", url, err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("daemon: OK (port %d)\n%s\n", port, string(body))
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 7823, "daemon HTTP port")
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
