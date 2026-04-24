package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/daemon"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon (SIGTERM via pidfile)",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			daemonHome := filepath.Join(home, ".mcp-gateway")
			pidPath := filepath.Join(daemonHome, "daemon.pid")
			pid, ok := readPid(pidPath)
			if !ok {
				return fmt.Errorf("no daemon running (no pidfile at %s)", pidPath)
			}
			if !processAlive(pid) {
				_ = os.Remove(pidPath)
				return fmt.Errorf("stale pidfile (pid=%d not running); removed", pid)
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return err
			}
			// Wait up to 5s for the socket to vanish.
			sock := daemon.ChooseSocketPath(daemonHome)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sock); err != nil && os.IsNotExist(err) {
					fmt.Printf("daemon stopped (pid=%d)\n", pid)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not exit within 5s (pid=%d)", pid)
		},
	}
}
