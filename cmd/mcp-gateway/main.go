package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the mcp-gateway daemon (long-running)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("daemon: not yet implemented")
		},
	}
}

func newStdioCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stdio",
		Short: "Run as a stdio bridge to the local daemon (spawn target for stdio-only clients)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("stdio: not yet implemented")
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("status: not yet implemented")
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
