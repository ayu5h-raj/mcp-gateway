package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a previously-disabled MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Post("/admin/servers/"+args[0]+"/enable", nil, nil); err != nil {
				return err
			}
			fmt.Printf("enabled %s\n", args[0])
			return nil
		},
	}
}
