package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop then start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If the running daemon is launchd-managed, the only correct
			// "restart" is `launchctl kickstart -k`. SIGTERM would race
			// launchd's respawn; manually starting after would conflict
			// with the still-attached launchd job. refuseIfLaunchdManaged
			// (defined in stop.go) returns an error with the right command.
			home, _ := os.UserHomeDir()
			pidPath := filepath.Join(home, ".mcp-gateway", "daemon.pid")
			if pid, ok := readPid(pidPath); ok && processAlive(pid) {
				if err := refuseIfLaunchdManaged(pid); err != nil {
					return err
				}
			}
			// Tolerate "no daemon running" from stop — proceed to start anyway.
			_ = newStopCmd().RunE(cmd, args)
			return newStartCmd().RunE(cmd, args)
		},
	}
}
