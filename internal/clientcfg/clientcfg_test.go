package clientcfg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}
	return abs
}

func sortedNames(srvs []Server) []string {
	out := make([]string, 0, len(srvs))
	for _, s := range srvs {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

func TestReadClaudeDesktop_Basic(t *testing.T) {
	srvs, err := readClaudeDesktop(loadFixture(t, "claude_desktop_basic.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := sortedNames(srvs)
	want := []string{"filesystem", "kite"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server names: got %v want %v", got, want)
	}
	for _, s := range srvs {
		if s.Command == "" {
			t.Fatalf("server %s has empty command", s.Name)
		}
		if !s.Enabled {
			t.Fatalf("server %s should default to enabled", s.Name)
		}
	}
}

func TestReadClaudeDesktop_Empty(t *testing.T) {
	srvs, err := readClaudeDesktop(loadFixture(t, "claude_desktop_empty.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(srvs) != 0 {
		t.Fatalf("want 0 servers, got %d", len(srvs))
	}
}

func TestReadClaudeDesktop_Missing(t *testing.T) {
	_, err := readClaudeDesktop("/nonexistent/path/claude.json")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !isMissing(err) {
		t.Fatalf("want ErrConfigMissing, got %v", err)
	}
}

func isMissing(err error) bool {
	for ; err != nil; err = unwrap(err) {
		if err == ErrConfigMissing {
			return true
		}
	}
	return false
}

func unwrap(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

func TestReadCursor_Basic(t *testing.T) {
	srvs, err := readCursor(loadFixture(t, "cursor_basic.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(srvs) != 1 || srvs[0].Name != "github" {
		t.Fatalf("want 1 server named 'github', got %#v", srvs)
	}
	if srvs[0].Env["GITHUB_TOKEN"] != "ghp_xxx" {
		t.Fatalf("env not propagated: %#v", srvs[0].Env)
	}
}

func TestDetect_SkipsMissing(t *testing.T) {
	// Detect uses os.UserHomeDir() + KnownClients(); we can't easily inject
	// a fake home without refactoring. Instead, verify that on a system where
	// neither config exists (CI), Detect returns no Detected entries.
	t.Setenv("HOME", t.TempDir())
	got := Detect()
	for _, d := range got {
		// On a tempdir HOME, no client configs should be found.
		if d.Err == nil {
			t.Fatalf("unexpected detection on empty home: %#v", d)
		}
	}
}

func TestPatch_PreservesUnknownKeys(t *testing.T) {
	// Copy fixture into a tempdir so Patch can mutate it.
	src := loadFixture(t, "claude_desktop_with_extras.json")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		t.Fatal(err)
	}
	c := Client{Name: "Claude Desktop", ID: "claude-desktop", ConfigPath: tmp}
	if err := Patch(c, []string{"kite"}, "/usr/local/bin/mcp-gateway"); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	// Read the result. Top-level keys other than mcpServers must survive.
	out, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got["globalShortcut"] != "Cmd+Shift+Space" {
		t.Fatalf("globalShortcut lost: %#v", got["globalShortcut"])
	}
	exp, ok := got["experimental"].(map[string]any)
	if !ok || exp["telemetryOptIn"] != false {
		t.Fatalf("experimental.telemetryOptIn lost: %#v", got["experimental"])
	}
	srvs, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing: %#v", got["mcpServers"])
	}
	if _, hasKite := srvs["kite"]; hasKite {
		t.Fatalf("kite should have been replaced, still present")
	}
	gw, ok := srvs["mcp-gateway"].(map[string]any)
	if !ok {
		t.Fatalf("mcp-gateway entry missing: %#v", srvs)
	}
	if gw["command"] != "/usr/local/bin/mcp-gateway" {
		t.Fatalf("gateway command wrong: %#v", gw["command"])
	}
	args, ok := gw["args"].([]any)
	if !ok || len(args) != 1 || args[0] != "stdio" {
		t.Fatalf("gateway args wrong: %#v", gw["args"])
	}
}

func TestPatch_BackupCreated(t *testing.T) {
	src := loadFixture(t, "claude_desktop_basic.json")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tmp := filepath.Join(dir, "config.json")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		t.Fatal(err)
	}
	c := Client{ID: "claude-desktop", ConfigPath: tmp}
	if err := Patch(c, []string{"kite"}, "/usr/bin/mcp-gateway"); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(tmp + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 backup file, got %d: %v", len(matches), matches)
	}
	bak, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bak, body) {
		t.Fatal("backup content does not match original")
	}
}
