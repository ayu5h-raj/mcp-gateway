package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
	"github.com/ayu5h-raj/mcp-gateway/internal/daemon"
	tuipkg "github.com/ayu5h-raj/mcp-gateway/internal/tui"
)

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive TUI for live observation and control of the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			daemonHome := filepath.Join(home, ".mcp-gateway")
			sock := daemon.ChooseSocketPath(daemonHome)
			if _, err := os.Stat(sock); err != nil {
				return fmt.Errorf("daemon not running (no socket at %s)", sock)
			}
			c := adminclient.New(sock)
			return tuipkg.Run(c, sock)
		},
	}
}
