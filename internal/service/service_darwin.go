//go:build darwin

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// fallbackPath is the safe baseline used when the user's login shell can't
// be probed. Covers Apple Silicon Homebrew, Intel Homebrew, and the system
// dirs — but NOT user-managed Node/Python installs (nvm, asdf, mise, etc).
const fallbackPath = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"

// userLoginPath returns the PATH a child process would inherit if launched
// from the user's login shell. Captures nvm/asdf/mise installs that aren't
// in standard system locations, so child MCP servers like `npx` resolve
// when the daemon is launched by launchd (which has a minimal env).
//
// Falls back to fallbackPath on any error or empty output. Bounded by 5s.
func userLoginPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return fallbackPath
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// -l = login shell (sources .zprofile / .bash_profile / etc.).
	// Some shells require an interactive flag to source rc files; -l alone
	// gets the user's PATH via login profile, which is what we want.
	out, err := exec.CommandContext(ctx, shell, "-l", "-c", "echo $PATH").Output()
	if err != nil {
		return fallbackPath
	}
	got := strings.TrimSpace(string(out))
	if got == "" {
		return fallbackPath
	}
	return got
}

// Install renders the plist using gatewayBinary, writes it atomically,
// and runs `launchctl bootstrap`. Idempotent: replaces existing plist
// and re-bootstraps. gatewayBinary must be an absolute path.
func Install(gatewayBinary string) error {
	if !filepath.IsAbs(gatewayBinary) {
		return fmt.Errorf("service: gatewayBinary must be absolute, got %q", gatewayBinary)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// Pre-create the log file's parent so launchd's StandardOutPath/
	// StandardErrorPath open succeeds. launchd does not create parent
	// dirs for these keys; without this, a `service install` run before
	// `mcp-gateway init` would respawn forever (KeepAlive=true) with no
	// log output anywhere.
	logDir := filepath.Join(home, ".mcp-gateway")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", logDir, err)
	}
	logFile := filepath.Join(logDir, "daemon.log")

	body, err := render(renderArgs{
		GatewayBinary: gatewayBinary,
		LogFile:       logFile,
		LoginPath:     userLoginPath(),
	})
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	path, err := PlistPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// If a previous version is loaded, bootout first (silent on failure).
	// Then give launchd a moment to actually tear down the service session
	// before bootstrapping a new one — bootout returns synchronously but
	// launchd processes the teardown asynchronously, so an immediate
	// bootstrap can race and fail with "Input/output error" (errno 5).
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+Label).Run()
	time.Sleep(250 * time.Millisecond)

	tmp, err := os.CreateTemp(dir, ".plist.tmp.*")
	if err != nil {
		return fmt.Errorf("tmp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}

	// Bootstrap with retry on transient post-bootout errors. launchd's
	// "Input/output error" (errno 5) and "service is already loaded"
	// (errno 37) both surface when the previous bootout hasn't fully
	// settled; both clear within a few hundred ms. Permanent errors
	// (bad plist, no permissions) also produce non-zero — we still try
	// twice in case the first attempt hit a coincidental I/O blip, then
	// give up with a useful annotation.
	const attempts = 3
	var lastOut []byte
	var lastErr error
	for i := 0; i < attempts; i++ {
		out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, path).CombinedOutput()
		if err == nil {
			return nil
		}
		lastOut, lastErr = out, err
		if i < attempts-1 {
			time.Sleep(time.Duration(250*(i+1)) * time.Millisecond)
		}
	}
	return fmt.Errorf(
		"launchctl bootstrap failed after %d attempts: %w (output: %s) — "+
			"this is a per-user gui/%s plist; sudo will not help. "+
			"Check the daemon log at ~/.mcp-gateway/daemon.log if launchd did manage to spawn it",
		attempts, lastErr, lastOut, uid,
	)
}

// Uninstall runs `launchctl bootout` and removes the plist file. Both
// steps are best-effort: a missing plist is a no-op, a not-loaded service
// is a no-op.
func Uninstall() error {
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+Label).Run()
	path, err := PlistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}
