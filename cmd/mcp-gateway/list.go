package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers and their state",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			c := adminclient.New(sock)
			var got []admin.ServerInfo
			if err := c.Get("/admin/servers", &got); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "NAME\tSTATE\tPREFIX\tTOOLS\t~TOKENS\tLAST ERROR")
			for _, s := range got {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t~%d\t%s\n", s.Name, s.State, s.Prefix, s.ToolCount, s.EstTokens, truncate(s.LastError, 40))
			}
			return tw.Flush()
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
