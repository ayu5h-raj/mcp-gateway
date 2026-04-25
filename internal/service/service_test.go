package service

// Install and Uninstall require a live launchctl session and cannot be unit-
// tested without a real macOS launchd; they are exercised by the manual smoke
// runbook (docs/release-runbook.md) and the Phase 8 e2e dry-run.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender_HasExpectedFields(t *testing.T) {
	out, err := render(renderArgs{
		GatewayBinary: "/usr/local/bin/mcp-gateway",
		LogFile:       "/Users/test/.mcp-gateway/daemon.log",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	checks := []string{
		"<key>Label</key><string>com.ayu5h-raj.mcp-gateway</string>",
		"<string>/usr/local/bin/mcp-gateway</string>",
		"<string>daemon</string>",
		"<key>KeepAlive</key><true/>",
		"<key>RunAtLoad</key><true/>",
		"<string>/Users/test/.mcp-gateway/daemon.log</string>",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("rendered plist missing %q\noutput:\n%s", c, out)
		}
	}
}

func TestPlistPath_NotEmpty(t *testing.T) {
	t.Setenv("HOME", "/Users/test")
	p, err := PlistPath()
	if err != nil {
		t.Fatalf("PlistPath: %v", err)
	}
	want := "/Users/test/Library/LaunchAgents/com.ayu5h-raj.mcp-gateway.plist"
	if p != want {
		t.Fatalf("want %s, got %s", want, p)
	}
}

func TestGetStatus_PlistAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if s.PlistInstalled {
		t.Fatal("PlistInstalled should be false on empty home")
	}
	if s.LaunchdLoaded {
		t.Fatal("LaunchdLoaded should be false on empty home")
	}
}

func TestGetStatus_PlistPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Label+".plist")
	if err := os.WriteFile(path, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !s.PlistInstalled {
		t.Fatal("PlistInstalled should be true")
	}
	if s.PlistPath != path {
		t.Fatalf("PlistPath: want %s, got %s", path, s.PlistPath)
	}
	// LaunchdLoaded is racy across CI / dev systems — don't assert here.
}
