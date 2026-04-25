package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/service"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the launchd auto-start service (macOS)",
	}
	cmd.AddCommand(newServiceInstallCmd())
	cmd.AddCommand(newServiceUninstallCmd())
	cmd.AddCommand(newServiceStatusCmd())
	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the launchd plist and bootstrap the service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return errors.New("service install: macOS only (Linux systemd unit is planned for v1.1; for now run `mcp-gateway start` from your shell rc)")
			}
			gw, err := resolveGatewayBinary()
			if err != nil {
				return err
			}
			if err := service.Install(gw); err != nil {
				return fmt.Errorf("install: %w", err)
			}
			fmt.Println("✓ service installed and loaded")
			return nil
		},
	}
}

func newServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the launchd plist and stop the service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return errors.New("service uninstall: macOS only")
			}
			if err := service.Uninstall(); err != nil {
				return fmt.Errorf("uninstall: %w", err)
			}
			fmt.Println("✓ service removed")
			return nil
		},
	}
}

func newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the launchd service install + load status",
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := service.GetStatus()
			if err != nil {
				return err
			}
			if runtime.GOOS != "darwin" {
				fmt.Println("service: macOS only — on Linux, run `mcp-gateway start` from your shell rc.")
				fmt.Println("         systemd unit support is planned for v1.1.")
				return nil
			}
			if !s.PlistInstalled {
				fmt.Println("service: not installed")
				return nil
			}
			fmt.Println("service:        installed")
			loaded := "not loaded"
			if s.LaunchdLoaded {
				if s.PID > 0 {
					loaded = fmt.Sprintf("loaded (pid %d)", s.PID)
				} else {
					loaded = "loaded"
				}
			}
			fmt.Printf("launchd:        %s\n", loaded)
			fmt.Printf("plist:          %s\n", s.PlistPath)
			home, _ := os.UserHomeDir()
			fmt.Printf("log:            %s\n", filepath.Join(home, ".mcp-gateway", "daemon.log"))
			return nil
		},
	}
}

// resolveGatewayBinary returns the absolute, symlink-resolved path of the
// running mcp-gateway binary. This is what we want in the plist so the
// service keeps working even if PATH changes.
func resolveGatewayBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("os.Executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil // best-effort fallback
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return resolved, nil
	}
	return abs, nil
}
