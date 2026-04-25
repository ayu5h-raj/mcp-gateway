// Package service manages the macOS launchd plist that auto-starts the
// mcp-gateway daemon on user login.
package service

import (
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
)

// Label is the launchd Label and the basename for the plist file.
// Locked in for v1.0.0 — renaming is a breaking change for installed users.
const Label = "com.ayu5h-raj.mcp-gateway"

// ErrUnsupported is returned by Install/Uninstall on non-macOS platforms.
var ErrUnsupported = errors.New("service: macOS only — see `mcp-gateway service status` for alternatives")

//go:embed plist.tmpl
var plistTmpl string

// renderArgs are the values the plist template needs.
type renderArgs struct {
	GatewayBinary string
	LogFile       string
}

func render(a renderArgs) (string, error) {
	t, err := template.New("plist").Parse(plistTmpl)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, a); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// PlistPath returns the absolute path the plist would live at for the
// current user: ~/Library/LaunchAgents/<Label>.plist.
func PlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// Status reports the install / load state for the launchd service.
type Status struct {
	PlistInstalled bool
	LaunchdLoaded  bool
	PID            int    // 0 if not running
	PlistPath      string // populated when known
}

// GetStatus reads the plist presence and asks launchctl whether it's loaded.
// On non-macOS the launchd check is skipped (LaunchdLoaded stays false).
func GetStatus() (Status, error) {
	path, err := PlistPath()
	if err != nil {
		return Status{}, err
	}
	out := Status{PlistPath: path}
	if _, err := os.Stat(path); err == nil {
		out.PlistInstalled = true
	}
	if runtime.GOOS != "darwin" {
		return out, nil
	}
	out.LaunchdLoaded, out.PID = launchctlPrintStatus()
	return out, nil
}

// launchctlPrintStatus runs `launchctl print gui/<uid>/<Label>` and parses
// loaded + pid. Returns (false, 0) when launchctl returns non-zero (not
// loaded). Best-effort: any parse error returns (false, 0) silently.
func launchctlPrintStatus() (bool, int) {
	uid := strconv.Itoa(os.Getuid())
	cmd := exec.Command("launchctl", "print", "gui/"+uid+"/"+Label)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, 0
	}
	loaded := true
	pid := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// pid = 12345
		if strings.HasPrefix(line, "pid = ") {
			if n, err := strconv.Atoi(strings.TrimPrefix(line, "pid = ")); err == nil {
				pid = n
			}
		}
	}
	return loaded, pid
}
