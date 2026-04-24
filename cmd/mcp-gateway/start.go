package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/daemon"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon as a detached background process",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			daemonHome := filepath.Join(home, ".mcp-gateway")
			pidPath := filepath.Join(daemonHome, "daemon.pid")
			if pid, ok := readPid(pidPath); ok && processAlive(pid) {
				return fmt.Errorf("daemon already running (pid=%d)", pid)
			}
			selfPath, err := os.Executable()
			if err != nil {
				return err
			}
			// Ensure ~/.mcp-gateway exists before opening the log file.
			if err := os.MkdirAll(daemonHome, 0o700); err != nil {
				return err
			}
			logPath := filepath.Join(daemonHome, "daemon.log")
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			cmd := exec.Command(selfPath, "daemon")
			cmd.Stdout = f
			cmd.Stderr = f
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				_ = f.Close()
				return err
			}
			_ = cmd.Process.Release()
			// Wait up to 5s for the socket to appear.
			sock := daemon.ChooseSocketPath(daemonHome)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sock); err == nil {
					// Read pid from the daemon's pidfile (cmd.Process.Pid is
					// stale after Release on some platforms).
					pid, _ := readPid(pidPath)
					fmt.Printf("daemon started (pid=%d, log=%s)\n", pid, logPath)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon failed to come up within 5s; check %s", logPath)
		},
	}
}

func readPid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
