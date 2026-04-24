package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an MCP server from the gateway config",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Delete("/admin/servers/" + args[0]); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", args[0])
			return nil
		},
	}
}
