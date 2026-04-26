package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/service"
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
			if err := refuseIfLaunchdManaged(pid); err != nil {
				return err
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return err
			}
			// Poll the pid for death rather than the socket for absence.
			// A launchd-managed (or otherwise auto-restarted) daemon recreates
			// the socket within milliseconds of a SIGTERM, which would falsely
			// look like "did not exit". The pid is the unambiguous signal.
			//
			// 10s headroom: the daemon SIGTERMs its MCP children on shutdown,
			// and some (e.g. mcp-remote/npx wrappers) take a few seconds to
			// drain stdio buffers before exiting.
			const stopTimeout = 10 * time.Second
			deadline := time.Now().Add(stopTimeout)
			for time.Now().Before(deadline) {
				if !processAlive(pid) {
					fmt.Printf("daemon stopped (pid=%d)\n", pid)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not exit within %s (pid=%d)", stopTimeout, pid)
		},
	}
}

// refuseIfLaunchdManaged returns an error explaining the right command if
// the running daemon is owned by launchd. SIGTERM against a launchd-managed
// daemon "succeeds" but launchd re-spawns within milliseconds, so the user's
// intent (stop the daemon) is not actually achievable via `mcp-gateway stop`.
func refuseIfLaunchdManaged(pid int) error {
	s, err := service.GetStatus()
	if err != nil {
		return nil // best-effort: don't fail stop because launchctl is missing
	}
	if !s.LaunchdLoaded || s.PID != pid {
		return nil
	}
	return fmt.Errorf(
		"daemon is managed by launchd (pid=%d) — SIGTERM would just trigger an immediate respawn.\n"+
			"  to stop and remove auto-start:  mcp-gateway service uninstall\n"+
			"  to restart in place:            launchctl kickstart -k gui/$(id -u)/%s",
		pid, service.Label)
}
