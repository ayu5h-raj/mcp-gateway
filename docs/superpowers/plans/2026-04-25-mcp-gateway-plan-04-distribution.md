# mcp-gateway — Plan 04: Distribution → v1.0.0

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Each phase has clear files + code blocks. Steps use checkbox (`- [ ]`) syntax for tracking. **TDD always**: failing test → run-and-fail → implement → run-and-pass → commit.

**Goal:** Ship v1.0.0 of `mcp-gateway` — `brew install ayu5h-raj/tap/mcp-gateway && mcp-gateway init` brings up a fully migrated, auto-starting daemon in one terminal session for any macOS user. Linux users get binaries via goreleaser and `mcp-gateway start`.

**Architecture:** Three new internal packages (`clientcfg`, `service`) plus two new cobra subcommands (`init`, `service`). Goreleaser-driven release pipeline triggered by `v*.*.*` tags via GitHub Actions, publishing four binaries to GitHub Releases, auto-bumping the existing Homebrew formula in `github.com/ayu5h-raj/homebrew-tap`, and attaching a hand-authored `install.sh` for non-brew users.

**Tech Stack:** Go 1.25 (no new core deps; `golang.org/x/term` for TTY detect is already pulled in transitively by bubbletea). Goreleaser v2. GitHub Actions. macOS launchd. POSIX sh for the installer.

**Reference:** spec `docs/superpowers/specs/2026-04-25-mcp-gateway-distribution-design.md`. Existing patterns: `internal/configwrite` (atomic JSON write), `internal/secret` (build-tagged platform code), `cmd/mcp-gateway/{add,rm,...}.go` (cobra subcommand style).

**v1.0.0 success criterion:** all of the following from a fresh macOS:

```sh
brew install ayu5h-raj/tap/mcp-gateway
mcp-gateway init -y                                   # imports + patches + installs service
launchctl print gui/$(id -u)/com.ayu5h-raj.mcp-gateway   # exit 0
mcp-gateway status                                    # daemon: OK
```

**Not in this plan:** Linux systemd unit, macOS notarization, in-binary auto-updater, Windows. All deferred per spec §1.

---

## File Structure

```
mcp-gateway/
├── cmd/mcp-gateway/
│   ├── main.go                  # MODIFY: register newInitCmd() + newServiceCmd()
│   ├── init.go                  # NEW: mcp-gateway init wizard
│   ├── service.go               # NEW: mcp-gateway service install|uninstall|status
│   └── prompt.go                # NEW: shared TTY-aware prompt helper
├── internal/clientcfg/
│   ├── clientcfg.go             # NEW: Client, Server, KnownClients, Detect
│   ├── claude_desktop.go        # NEW: Claude Desktop reader/writer
│   ├── cursor.go                # NEW: Cursor reader/writer
│   ├── clientcfg_test.go        # NEW: table-driven tests + fixture loaders
│   └── testdata/
│       ├── claude_desktop_basic.json    # NEW
│       ├── claude_desktop_with_extras.json  # NEW (preserves unknown keys)
│       ├── claude_desktop_empty.json    # NEW
│       └── cursor_basic.json            # NEW
├── internal/service/
│   ├── service.go               # NEW: PlistPath, GetStatus, render, ErrUnsupported
│   ├── service_darwin.go        # NEW: Install / Uninstall via launchctl
│   ├── service_other.go         # NEW: stubs returning ErrUnsupported (build tag !darwin)
│   ├── plist.tmpl               # NEW: launchd plist template (embedded)
│   └── service_test.go          # NEW: render golden + status parsing
├── scripts/
│   └── install.sh               # NEW: POSIX-sh installer
├── .goreleaser.yaml             # NEW
├── .github/workflows/
│   └── release.yaml             # NEW
├── docs/
│   └── release-runbook.md       # NEW: how to cut releases, rotate tokens
├── CHANGELOG.md                 # NEW
└── README.md                    # MODIFY: rewrite Quick Start + add install / uninstall / service sections

[separate repo]
homebrew-tap/
└── Formula/
    └── mcp-gateway.rb           # NEW: hand-authored placeholder; goreleaser auto-bumps later
```

Total: 13 new files, 2 modified. ~1500 LOC including tests + templates.

---

## Phase 1 — `internal/clientcfg` (detect & rewrite client configs)

**Phase deliverable:** a pure-Go package that can list known MCP clients on this OS, parse their `mcpServers` blocks (returning servers + any parse errors), and atomically rewrite the config to replace named servers with a single mcp-gateway entry — preserving every other top-level key.

### Task 1.1: Package skeleton + types

**Files:**
- Create: `internal/clientcfg/clientcfg.go`

- [ ] **Step 1: Write the file**

```go
// Package clientcfg detects and rewrites the MCP server lists in well-known
// client configs (Claude Desktop, Cursor) without disturbing other keys.
package clientcfg

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Client describes one supported MCP client and where to find its config.
type Client struct {
	Name       string // human-readable: "Claude Desktop", "Cursor"
	ID         string // stable ID: "claude-desktop", "cursor"
	ConfigPath string // absolute path on this machine
}

// Server is one downstream MCP server entry from a client's config.
type Server struct {
	Name    string            `json:"-"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"-"` // not in client schema; default true
}

// Detected groups one client with its parsed server list (or a parse error).
type Detected struct {
	Client  Client
	Servers []Server
	Err     error // non-nil if the file existed but failed to parse
}

// ErrConfigMissing means the client's config file does not exist on disk.
// Callers should treat this as "client not installed" and skip it silently.
var ErrConfigMissing = errors.New("client config missing")

// KnownClients returns the list of clients we know how to read on this OS.
func KnownClients() []Client {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return []Client{
			{
				Name:       "Claude Desktop",
				ID:         "claude-desktop",
				ConfigPath: filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
			},
			{
				Name:       "Cursor",
				ID:         "cursor",
				ConfigPath: filepath.Join(home, ".cursor", "mcp.json"),
			},
		}
	case "linux":
		// Claude Desktop on Linux uses XDG; Cursor uses ~/.cursor.
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		return []Client{
			{
				Name:       "Claude Desktop",
				ID:         "claude-desktop",
				ConfigPath: filepath.Join(xdg, "Claude", "claude_desktop_config.json"),
			},
			{
				Name:       "Cursor",
				ID:         "cursor",
				ConfigPath: filepath.Join(home, ".cursor", "mcp.json"),
			},
		}
	}
	return nil
}

// Detect reads each known client's config and returns one Detected per
// client whose file exists. Missing files are skipped (no entry returned);
// parse errors return an entry with Err set and Servers nil.
func Detect() []Detected {
	var out []Detected
	for _, c := range KnownClients() {
		servers, err := readClient(c)
		if errors.Is(err, ErrConfigMissing) {
			continue
		}
		out = append(out, Detected{Client: c, Servers: servers, Err: err})
	}
	return out
}

// readClient is dispatched to the per-client reader. Defined here so Detect
// can stay generic; per-client files implement the actual format.
func readClient(c Client) ([]Server, error) {
	switch c.ID {
	case "claude-desktop":
		return readClaudeDesktop(c.ConfigPath)
	case "cursor":
		return readCursor(c.ConfigPath)
	default:
		return nil, errors.New("clientcfg: unknown client id: " + c.ID)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/clientcfg/...`
Expected: build fails — `readClaudeDesktop` and `readCursor` are not defined yet. That's the next task.

- [ ] **Step 3: Commit (skip until Task 1.4 — the package isn't compilable yet)**

We commit this as part of Task 1.4 once the readers exist.

### Task 1.2: Claude Desktop reader + fixtures

**Files:**
- Create: `internal/clientcfg/claude_desktop.go`
- Create: `internal/clientcfg/testdata/claude_desktop_basic.json`
- Create: `internal/clientcfg/testdata/claude_desktop_with_extras.json`
- Create: `internal/clientcfg/testdata/claude_desktop_empty.json`
- Create: `internal/clientcfg/clientcfg_test.go` (initial — Claude Desktop tests only)

- [ ] **Step 1: Write the basic fixture**

`internal/clientcfg/testdata/claude_desktop_basic.json`:

```json
{
  "mcpServers": {
    "kite": {
      "command": "npx",
      "args": ["mcp-remote", "https://mcp.kite.trade/sse"]
    },
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/test"],
      "env": {"FOO": "bar"}
    }
  }
}
```

- [ ] **Step 2: Write the with-extras fixture**

`internal/clientcfg/testdata/claude_desktop_with_extras.json`:

```json
{
  "globalShortcut": "Cmd+Shift+Space",
  "mcpServers": {
    "kite": {
      "command": "npx",
      "args": ["mcp-remote", "https://mcp.kite.trade/sse"]
    }
  },
  "experimental": {
    "telemetryOptIn": false
  }
}
```

- [ ] **Step 3: Write the empty fixture**

`internal/clientcfg/testdata/claude_desktop_empty.json`:

```json
{}
```

- [ ] **Step 4: Write failing tests**

`internal/clientcfg/clientcfg_test.go`:

```go
package clientcfg

import (
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
```

- [ ] **Step 5: Run tests to verify they fail**

Run: `go test ./internal/clientcfg/... -run TestReadClaudeDesktop -v`
Expected: build error — `readClaudeDesktop` undefined.

- [ ] **Step 6: Implement the reader**

`internal/clientcfg/claude_desktop.go`:

```go
package clientcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// readClaudeDesktop parses the Claude Desktop config at path and returns
// the listed MCP servers. Returns ErrConfigMissing wrapped if path doesn't
// exist; other I/O or parse errors are returned wrapped with context.
func readClaudeDesktop(path string) ([]Server, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", path, ErrConfigMissing)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]Server `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make([]Server, 0, len(raw.MCPServers))
	for name, s := range raw.MCPServers {
		s.Name = name
		s.Enabled = true
		out = append(out, s)
	}
	return out, nil
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/clientcfg/... -run TestReadClaudeDesktop -v`
Expected: PASS — three tests green.

### Task 1.3: Cursor reader + fixture

**Files:**
- Create: `internal/clientcfg/cursor.go`
- Create: `internal/clientcfg/testdata/cursor_basic.json`
- Modify: `internal/clientcfg/clientcfg_test.go` — append Cursor tests

- [ ] **Step 1: Write the fixture**

`internal/clientcfg/testdata/cursor_basic.json`:

```json
{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {"GITHUB_TOKEN": "ghp_xxx"}
    }
  }
}
```

- [ ] **Step 2: Append failing tests**

Append to `internal/clientcfg/clientcfg_test.go`:

```go
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
```

- [ ] **Step 3: Verify failure**

Run: `go test ./internal/clientcfg/... -run TestReadCursor -v`
Expected: build error — `readCursor` undefined.

- [ ] **Step 4: Implement the reader**

`internal/clientcfg/cursor.go`:

```go
package clientcfg

// Cursor's mcp.json shares the exact schema as Claude Desktop's mcpServers
// block. Reuse the parser; if the schema diverges in future, split here.
func readCursor(path string) ([]Server, error) {
	return readClaudeDesktop(path)
}
```

- [ ] **Step 5: Verify pass**

Run: `go test ./internal/clientcfg/... -run TestReadCursor -v`
Expected: PASS.

### Task 1.4: `Detect` integration test + first commit

**Files:**
- Modify: `internal/clientcfg/clientcfg_test.go` — append Detect test using a fake home dir
- Create: nothing new

- [ ] **Step 1: Append the failing test**

Append to `internal/clientcfg/clientcfg_test.go`:

```go
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
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/clientcfg/... -v`
Expected: PASS — `Detect` already calls `readClient` which dispatches; missing files are filtered out.

- [ ] **Step 3: Run the full package test with race**

Run: `go test -race ./internal/clientcfg/...`
Expected: PASS.

- [ ] **Step 4: Commit Phase 1.1–1.4**

```bash
git add internal/clientcfg/
git commit -m "$(cat <<'EOF'
feat(clientcfg): detect MCP servers from Claude Desktop and Cursor configs

Pure parser/detector. Returns one Detected per known client whose config
exists; missing files skipped, parse errors propagated with path context.
Cursor uses the same schema as Claude Desktop.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 1.5: `Patch` — atomic rewrite preserving unknown keys

**Files:**
- Modify: `internal/clientcfg/claude_desktop.go` — append `patchClaudeDesktop`
- Modify: `internal/clientcfg/cursor.go` — append `patchCursor` (alias)
- Modify: `internal/clientcfg/clientcfg.go` — add public `Patch` dispatcher
- Modify: `internal/clientcfg/clientcfg_test.go` — append Patch tests

- [ ] **Step 1: Write failing tests**

Append to `internal/clientcfg/clientcfg_test.go`:

```go
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
```

Add the missing import:

```go
import (
	"bytes"      // ADD
	"encoding/json" // ADD
	// existing imports...
)
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/clientcfg/... -run TestPatch -v`
Expected: build error — `Patch` undefined.

- [ ] **Step 3: Implement `patchClaudeDesktop`**

Append to `internal/clientcfg/claude_desktop.go`:

```go
import (
	"path/filepath"
	"time"
)

// patchClaudeDesktop reads path, removes named servers, inserts an
// "mcp-gateway" stdio entry, writes via tmp+rename. Backs up the original
// to <path>.bak.<YYYYMMDD-HHMMSS> first.
func patchClaudeDesktop(path string, removedServers []string, gatewayBinary string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// Parse into a generic map so we preserve unknown top-level keys verbatim.
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if top == nil {
		top = map[string]any{}
	}
	srvsAny, _ := top["mcpServers"].(map[string]any)
	if srvsAny == nil {
		srvsAny = map[string]any{}
	}
	for _, name := range removedServers {
		delete(srvsAny, name)
	}
	srvsAny["mcp-gateway"] = map[string]any{
		"command": gatewayBinary,
		"args":    []any{"stdio"},
	}
	top["mcpServers"] = srvsAny

	// Backup first.
	bakPath := backupPath(path, time.Now())
	if err := os.WriteFile(bakPath, body, 0o600); err != nil {
		return fmt.Errorf("backup %s: %w", bakPath, err)
	}

	// Write atomically.
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	out = append(out, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".clientcfg.tmp.*")
	if err != nil {
		return fmt.Errorf("tmp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// backupPath returns a unique backup filename. If a file already exists at
// the natural <path>.bak.<timestamp>, append -1, -2, etc.
func backupPath(path string, now time.Time) string {
	base := fmt.Sprintf("%s.bak.%s", path, now.Format("20060102-150405"))
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	// Extreme edge case: 1000 backups in the same second. Just overwrite the natural one.
	return base
}
```

- [ ] **Step 4: Add Cursor patcher (alias)**

Append to `internal/clientcfg/cursor.go`:

```go
func patchCursor(path string, removedServers []string, gatewayBinary string) error {
	return patchClaudeDesktop(path, removedServers, gatewayBinary)
}
```

- [ ] **Step 5: Add the public dispatcher**

Append to `internal/clientcfg/clientcfg.go`:

```go
// Patch rewrites the named client's config to remove removedServers and
// install a single "mcp-gateway" stdio entry pointing at gatewayBinary.
// The original file is backed up to <path>.bak.<timestamp>; the rewrite
// itself is atomic (tmp + rename). Preserves all unknown top-level keys.
func Patch(c Client, removedServers []string, gatewayBinary string) error {
	switch c.ID {
	case "claude-desktop":
		return patchClaudeDesktop(c.ConfigPath, removedServers, gatewayBinary)
	case "cursor":
		return patchCursor(c.ConfigPath, removedServers, gatewayBinary)
	default:
		return errors.New("clientcfg: unknown client id: " + c.ID)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test -race ./internal/clientcfg/... -v`
Expected: all PASS.

- [ ] **Step 7: Lint + vet**

Run: `go vet ./internal/clientcfg/...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/clientcfg/
git commit -m "$(cat <<'EOF'
feat(clientcfg): atomic Patch that swaps named servers for mcp-gateway entry

Backs up the original config to <path>.bak.<timestamp>, parses into a
generic map (so unknown top-level keys like globalShortcut survive),
removes the named servers, inserts an mcp-gateway stdio entry, writes
via tmp + rename.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — `internal/service` (launchd plist management)

**Phase deliverable:** a Go package that renders the launchd plist from a template, can install/uninstall it via `launchctl bootstrap`/`bootout`, and reports its load status. macOS-only (Linux falls back to `ErrUnsupported`). Exposed via `mcp-gateway service install|uninstall|status`.

### Task 2.1: Plist template + render

**Files:**
- Create: `internal/service/plist.tmpl`
- Create: `internal/service/service.go`
- Create: `internal/service/service_test.go`

- [ ] **Step 1: Write the template**

`internal/service/plist.tmpl`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.ayu5h-raj.mcp-gateway</string>
    <key>ProgramArguments</key>
    <array>
      <string>{{.GatewayBinary}}</string>
      <string>daemon</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>{{.LogFile}}</string>
    <key>StandardErrorPath</key><string>{{.LogFile}}</string>
    <key>ProcessType</key><string>Background</string>
    <key>EnvironmentVariables</key>
    <dict>
      <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>
  </dict>
</plist>
```

- [ ] **Step 2: Write failing tests**

`internal/service/service_test.go`:

```go
package service

import (
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
```

- [ ] **Step 3: Verify failure**

Run: `go test ./internal/service/... -v`
Expected: build error — package empty.

- [ ] **Step 4: Implement service.go**

`internal/service/service.go`:

```go
// Package service manages the macOS launchd plist that auto-starts the
// mcp-gateway daemon on user login.
package service

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
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
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/service/... -v`
Expected: render + path tests PASS.

### Task 2.2: GetStatus

**Files:**
- Modify: `internal/service/service.go` — append `GetStatus`
- Modify: `internal/service/service_test.go` — append a status test that uses a fake plist file

- [ ] **Step 1: Append the failing test**

```go
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
```

Add the `path/filepath` import to the test file if not already present.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/service/... -run TestGetStatus -v`
Expected: build error — `GetStatus` undefined.

- [ ] **Step 3: Implement**

Append to `internal/service/service.go`:

```go
import (
	"os/exec"
	"runtime"
	"strconv"
)

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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/service/... -v`
Expected: PASS.

### Task 2.3: Install/Uninstall (build-tagged)

**Files:**
- Create: `internal/service/service_darwin.go`
- Create: `internal/service/service_other.go`

- [ ] **Step 1: Create the darwin file**

`internal/service/service_darwin.go`:

```go
//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

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
	logFile := filepath.Join(home, ".mcp-gateway", "daemon.log")

	body, err := render(renderArgs{
		GatewayBinary: gatewayBinary,
		LogFile:       logFile,
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
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+Label).Run()

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

	out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w (output: %s)", err, out)
	}
	return nil
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
```

- [ ] **Step 2: Create the other-OS stubs**

`internal/service/service_other.go`:

```go
//go:build !darwin

package service

// Install returns ErrUnsupported on non-macOS platforms.
func Install(gatewayBinary string) error { return ErrUnsupported }

// Uninstall returns ErrUnsupported on non-macOS platforms.
func Uninstall() error { return ErrUnsupported }
```

- [ ] **Step 3: Verify build on both platforms**

Run: `go build ./internal/service/...`
Expected: clean.

Run: `GOOS=linux go build ./internal/service/...`
Expected: clean.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/service/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/
git commit -m "$(cat <<'EOF'
feat(service): launchd plist install/uninstall/status (macOS)

Renders the plist from an embedded template, writes via tmp+rename,
runs launchctl bootstrap. Uninstall runs bootout and removes the file;
both are idempotent. Linux returns ErrUnsupported with a friendly
message; the cobra subcommand catches this and prints the alternative
(mcp-gateway start in shell rc, systemd unit deferred to v1.1).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 2.4: Cobra subcommand `service install|uninstall|status`

**Files:**
- Create: `cmd/mcp-gateway/service.go`
- Modify: `cmd/mcp-gateway/main.go` — register `newServiceCmd()`

- [ ] **Step 1: Write the subcommand**

`cmd/mcp-gateway/service.go`:

```go
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
```

- [ ] **Step 2: Register in main**

Modify `cmd/mcp-gateway/main.go` — add to `newRootCmd`:

```go
root.AddCommand(newServiceCmd())
```

(Place it next to `newTUICmd()`.)

- [ ] **Step 3: Build + smoke**

Run: `make build && ./bin/mcp-gateway service status`
Expected: prints `service: not installed` (assuming no prior install) and exits 0.

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp-gateway/service.go cmd/mcp-gateway/main.go
git commit -m "$(cat <<'EOF'
feat(cli): mcp-gateway service install|uninstall|status subcommand

Wraps internal/service. macOS-only for v1.0; Linux prints an explanation.
Resolves the binary path via os.Executable + EvalSymlinks so the plist
records the canonical path even if mcp-gateway later moves.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — `mcp-gateway init` (the wizard)

**Phase deliverable:** `mcp-gateway init` (no flags) interactively walks through detect → confirm import → confirm patch → confirm service install. With `-y`/`--yes`, fully non-interactive. With `--no-import`, skips detection and writes empty config. With `--no-patch` and `--no-service`, skips those steps. Reuses `clientcfg.Detect`, `clientcfg.Patch`, `service.Install`, and `internal/configwrite`.

### Task 3.1: TTY-aware prompt helper

**Files:**
- Create: `cmd/mcp-gateway/prompt.go`
- Create: `cmd/mcp-gateway/prompt_test.go`

- [ ] **Step 1: Write failing tests**

`cmd/mcp-gateway/prompt_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirm_DefaultYesAcceptsEmpty(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := confirmFromReader(in, &out, "Patch?", true, false)
	if got != true {
		t.Fatalf("want true, got false")
	}
	if !strings.Contains(out.String(), "[Y/n]") {
		t.Fatalf("prompt should show [Y/n], got %q", out.String())
	}
}

func TestConfirm_DefaultNoRejectsEmpty(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := confirmFromReader(in, &out, "Force?", false, false)
	if got != false {
		t.Fatalf("want false, got true")
	}
	if !strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("prompt should show [y/N], got %q", out.String())
	}
}

func TestConfirm_AcceptsYn(t *testing.T) {
	cases := map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YES\n": true,
		"n\n": false, "N\n": false, "no\n": false, "NO\n": false,
	}
	for input, want := range cases {
		in := strings.NewReader(input)
		var out bytes.Buffer
		got := confirmFromReader(in, &out, "?", true, false)
		if got != want {
			t.Errorf("input %q: want %v, got %v", input, want, got)
		}
	}
}

func TestConfirm_AssumeYesShortCircuits(t *testing.T) {
	in := strings.NewReader("garbage that would otherwise fail\n")
	var out bytes.Buffer
	got := confirmFromReader(in, &out, "?", false, true)
	if got != true {
		t.Fatal("assumeYes should always return true")
	}
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./cmd/mcp-gateway/... -run TestConfirm -v`
Expected: build error — `confirmFromReader` undefined.

- [ ] **Step 3: Implement**

`cmd/mcp-gateway/prompt.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirm prints prompt + a [Y/n] (defaultYes) or [y/N] suffix and reads
// a yes/no answer from stdin. If assumeYes is true, returns true without
// reading. If stdin is not a TTY and assumeYes is false, returns the
// default — never blocks on a piped command line.
func confirm(prompt string, defaultYes, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return defaultYes
	}
	return confirmFromReader(os.Stdin, os.Stdout, prompt, defaultYes, false)
}

// confirmFromReader is the testable core: reads one line from in, writes
// the prompt to out, returns the decision. Empty input picks defaultYes.
// Anything starting with y/Y is yes; n/N is no; other input re-prompts up
// to 3 times then falls back to defaultYes.
func confirmFromReader(in io.Reader, out io.Writer, prompt string, defaultYes, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	br := bufio.NewReader(in)
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(out, "%s %s ", prompt, suffix)
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			return defaultYes
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch {
		case line == "":
			return defaultYes
		case line == "y" || line == "yes":
			return true
		case line == "n" || line == "no":
			return false
		default:
			fmt.Fprintln(out, "please answer y or n")
		}
	}
	return defaultYes
}
```

- [ ] **Step 4: Add the dep**

Run: `go get golang.org/x/term && go mod tidy`
Expected: clean.

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/mcp-gateway/... -run TestConfirm -v`
Expected: PASS.

### Task 3.2: `init` command shell + flags + early validation

**Files:**
- Create: `cmd/mcp-gateway/init.go`
- Modify: `cmd/mcp-gateway/main.go` — register `newInitCmd()`

- [ ] **Step 1: Skeleton**

`cmd/mcp-gateway/init.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/clientcfg"
	"github.com/ayu5h-raj/mcp-gateway/internal/config"
	"github.com/ayu5h-raj/mcp-gateway/internal/configwrite"
	"github.com/ayu5h-raj/mcp-gateway/internal/service"
)

func newInitCmd() *cobra.Command {
	var (
		noImport  bool
		noPatch   bool
		noService bool
		force     bool
		assumeYes bool
		cfgPath   string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "First-run wizard: detect MCP clients, migrate servers, install service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cfgPath == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				cfgPath = filepath.Join(home, ".mcp-gateway", "config.jsonc")
			}
			gw, err := resolveGatewayBinary()
			if err != nil {
				return fmt.Errorf("resolve gateway binary: %w", err)
			}
			if err := refuseIfConfigured(cfgPath, force); err != nil {
				return err
			}
			fmt.Println("mcp-gateway init — first-run wizard")
			fmt.Println()
			imported, importedFromClient, err := importStep(cfgPath, noImport, assumeYes)
			if err != nil {
				return err
			}
			if !noPatch {
				if err := patchStep(importedFromClient, gw, assumeYes); err != nil {
					return err
				}
			}
			if !noService {
				if err := serviceStep(gw, assumeYes); err != nil {
					return err
				}
			}
			printFooter(imported)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noImport, "no-import", false, "skip detection and write an empty config")
	cmd.Flags().BoolVar(&noPatch, "no-patch", false, "import but don't modify any client config")
	cmd.Flags().BoolVar(&noService, "no-service", false, "don't install the launchd auto-start service")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing non-empty config")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "accept all prompts (non-interactive)")
	cmd.Flags().StringVar(&cfgPath, "config", "", "config destination (default ~/.mcp-gateway/config.jsonc)")
	return cmd
}

// refuseIfConfigured aborts when the target config already exists with
// non-empty mcpServers and --force was not passed.
func refuseIfConfigured(cfgPath string, force bool) error {
	if force {
		return nil
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read existing config: %w", err)
	}
	if len(body) == 0 {
		return nil
	}
	cfg, err := config.ParseFile(cfgPath)
	if err != nil {
		// Existing file but unparseable: refuse to silently destroy it.
		return fmt.Errorf("existing config at %s is unparseable: %w (use --force to overwrite)", cfgPath, err)
	}
	if len(cfg.MCPServers) > 0 {
		return fmt.Errorf("mcp-gateway is already configured at %s with %d server(s). Use --force to overwrite, or `mcp-gateway add` to add more", cfgPath, len(cfg.MCPServers))
	}
	return nil
}

func printFooter(imported int) {
	fmt.Println()
	if imported > 0 {
		fmt.Println("mcp-gateway is running. Restart any patched client to pick up the new config.")
	} else {
		fmt.Println("mcp-gateway is configured. Use `mcp-gateway add` to add servers.")
	}
	fmt.Println()
	fmt.Println("Useful commands:")
	fmt.Println("  mcp-gateway tui              live ops dashboard")
	fmt.Println("  mcp-gateway list             show servers and tool counts")
	fmt.Println("  mcp-gateway add <name> ...   add a new server")
	if runtime.GOOS == "darwin" {
		fmt.Println("  mcp-gateway service status   check the launchd service")
	}
}

// importStep, patchStep, serviceStep are filled in by Task 3.3, 3.4, 3.5.
func importStep(cfgPath string, noImport, assumeYes bool) (int, []importedFromClient, error) {
	return 0, nil, errors.New("not implemented")
}

type importedFromClient struct {
	client  clientcfg.Client
	servers []string // names imported (for the patch step)
}

func patchStep(imported []importedFromClient, gw string, assumeYes bool) error {
	return errors.New("not implemented")
}

func serviceStep(gw string, assumeYes bool) error {
	return errors.New("not implemented")
}
```

- [ ] **Step 2: Register in main**

Modify `cmd/mcp-gateway/main.go` — add to `newRootCmd`:

```go
root.AddCommand(newInitCmd())
```

(Place near `newServiceCmd()`.)

- [ ] **Step 3: Build to verify wiring**

Run: `go build ./cmd/mcp-gateway/`
Expected: clean.

### Task 3.3: Implement `importStep`

- [ ] **Step 1: Replace the stub**

In `cmd/mcp-gateway/init.go`, replace the `importStep` stub with:

```go
func importStep(cfgPath string, noImport, assumeYes bool) (int, []importedFromClient, error) {
	// Ensure the config directory exists.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return 0, nil, fmt.Errorf("mkdir config dir: %w", err)
	}
	// Build the new Config.
	cfg := &config.Config{
		Version: 1,
		Daemon: config.Daemon{
			HTTPPort:                      7823,
			LogLevel:                      "info",
			EventBufferSize:               10000,
			ChildRestartBackoffMaxSeconds: 60,
			ChildRestartMaxAttempts:       5,
		},
		MCPServers: map[string]config.Server{},
	}
	var importedClients []importedFromClient
	totalImported := 0

	if !noImport {
		detected := clientcfg.Detect()
		if len(detected) == 0 {
			fmt.Println("No MCP clients detected. Writing an empty config.")
		} else {
			fmt.Println("Detected MCP clients:")
			for _, d := range detected {
				if d.Err != nil {
					fmt.Printf("  • %s — parse error: %v (skipping)\n", d.Client.Name, d.Err)
					continue
				}
				fmt.Printf("  • %s\n", d.Client.Name)
				if len(d.Servers) == 0 {
					fmt.Println("      (no servers configured)")
					continue
				}
				for _, s := range d.Servers {
					argsStr := ""
					if len(s.Args) > 0 {
						argsStr = " " + joinArgs(s.Args)
					}
					fmt.Printf("      %-15s — %s%s\n", s.Name, s.Command, argsStr)
				}
			}
			fmt.Println()
			for _, d := range detected {
				if d.Err != nil || len(d.Servers) == 0 {
					continue
				}
				prompt := fmt.Sprintf("Import %d server(s) from %s?", len(d.Servers), d.Client.Name)
				if !confirm(prompt, true, assumeYes) {
					continue
				}
				names := make([]string, 0, len(d.Servers))
				for _, s := range d.Servers {
					if _, exists := cfg.MCPServers[s.Name]; exists {
						fmt.Printf("  ⚠ skipping %s (already in config)\n", s.Name)
						continue
					}
					cfg.MCPServers[s.Name] = config.Server{
						Command: s.Command,
						Args:    s.Args,
						Env:     s.Env,
						Enabled: true,
					}
					names = append(names, s.Name)
					totalImported++
				}
				importedClients = append(importedClients, importedFromClient{
					client:  d.Client,
					servers: names,
				})
			}
		}
	}

	// Write the config atomically. configwrite.Apply parses-then-mutates an
	// existing file; for init we may have nothing on disk yet, so handle the
	// missing-file case by writing it directly. Then re-validate via Apply
	// to make sure round-trip is clean.
	if err := writeFreshConfig(cfgPath, cfg); err != nil {
		return 0, nil, fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("  ✓ wrote %s\n", cfgPath)
	return totalImported, importedClients, nil
}

func writeFreshConfig(path string, cfg *config.Config) error {
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
```

Add the missing import:

```go
import (
	"encoding/json" // ADD
	// existing imports...
)
```

- [ ] **Step 2: Build**

Run: `go build ./cmd/mcp-gateway/`
Expected: clean.

- [ ] **Step 3: Quick smoke (manual)**

Run: `mkdir -p /tmp/mgw-test && ./bin/mcp-gateway init --no-patch --no-service --config /tmp/mgw-test/config.jsonc -y && cat /tmp/mgw-test/config.jsonc && rm -rf /tmp/mgw-test`
Expected: detects no clients (or detects existing ones), writes a minimal config, prints footer, exits 0.

### Task 3.4: Implement `patchStep`

- [ ] **Step 1: Replace the stub**

In `cmd/mcp-gateway/init.go`:

```go
func patchStep(imported []importedFromClient, gw string, assumeYes bool) error {
	if len(imported) == 0 {
		return nil
	}
	for _, ifc := range imported {
		if len(ifc.servers) == 0 {
			continue
		}
		prompt := fmt.Sprintf("Patch %s's config to point at the gateway?", ifc.client.Name)
		if !confirm(prompt, true, assumeYes) {
			fmt.Printf("  Skipped %s. To do it manually, replace its mcpServers with:\n", ifc.client.Name)
			fmt.Printf("    \"mcp-gateway\": { \"command\": %q, \"args\": [\"stdio\"] }\n", gw)
			continue
		}
		if err := clientcfg.Patch(ifc.client, ifc.servers, gw); err != nil {
			return fmt.Errorf("patch %s: %w", ifc.client.Name, err)
		}
		fmt.Printf("  ✓ backed up + patched %s\n", ifc.client.ConfigPath)
	}
	return nil
}
```

- [ ] **Step 2: Build**

Run: `go build ./cmd/mcp-gateway/`
Expected: clean.

### Task 3.5: Implement `serviceStep`

- [ ] **Step 1: Replace the stub**

In `cmd/mcp-gateway/init.go`:

```go
func serviceStep(gw string, assumeYes bool) error {
	if runtime.GOOS != "darwin" {
		fmt.Println("Skipping auto-start: macOS only for v1.0 (run `mcp-gateway start` from your shell rc on Linux).")
		return nil
	}
	if !confirm("Auto-start mcp-gateway on login (recommended)?", true, assumeYes) {
		fmt.Println("  Skipped. You can install the service later with `mcp-gateway service install`.")
		return nil
	}
	if err := service.Install(gw); err != nil {
		return fmt.Errorf("service install: %w", err)
	}
	fmt.Println("  ✓ launchd service installed and loaded")
	return nil
}
```

- [ ] **Step 2: Build + run full smoke**

Run: `go build ./cmd/mcp-gateway/`
Expected: clean.

- [ ] **Step 3: Run unit tests across the whole repo**

Run: `make test`
Expected: PASS.

### Task 3.6: End-to-end init test

**Files:**
- Create: `cmd/mcp-gateway/init_test.go`

- [ ] **Step 1: Write the test**

`cmd/mcp-gateway/init_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

func TestInit_NoImportNoServiceNoPatch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	// Direct call into the import step at the package level.
	imported, _, err := importStep(cfgPath, true /*noImport*/, true /*assumeYes*/)
	if err != nil {
		t.Fatalf("importStep: %v", err)
	}
	if imported != 0 {
		t.Fatalf("expected 0 imports, got %d", imported)
	}
	// Verify the config was written and parses.
	cfg, err := config.ParseFile(cfgPath)
	if err != nil {
		t.Fatalf("parse written config: %v", err)
	}
	if cfg.Daemon.HTTPPort != 7823 {
		t.Fatalf("default port not set: %#v", cfg.Daemon)
	}
	if len(cfg.MCPServers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(cfg.MCPServers))
	}
}

func TestRefuseIfConfigured_SkipsEmptyMCPServers(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	body := []byte(`{"version":1,"daemon":{"http_port":7823,"log_level":"info"},"mcpServers":{}}`)
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseIfConfigured(cfgPath, false); err != nil {
		t.Fatalf("should NOT refuse on empty mcpServers: %v", err)
	}
}

func TestRefuseIfConfigured_RefusesNonEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	body := []byte(`{"version":1,"daemon":{"http_port":7823,"log_level":"info"},"mcpServers":{"x":{"command":"echo","enabled":true}}}`)
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseIfConfigured(cfgPath, false); err == nil {
		t.Fatal("should refuse on non-empty mcpServers")
	}
	// --force overrides
	if err := refuseIfConfigured(cfgPath, true); err != nil {
		t.Fatalf("--force should override: %v", err)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test -race ./cmd/mcp-gateway/... -v`
Expected: PASS.

- [ ] **Step 3: Commit Phase 3**

```bash
git add cmd/mcp-gateway/init.go cmd/mcp-gateway/init_test.go cmd/mcp-gateway/prompt.go cmd/mcp-gateway/prompt_test.go cmd/mcp-gateway/main.go go.mod go.sum
git commit -m "$(cat <<'EOF'
feat(cli): mcp-gateway init — first-run wizard

Detects Claude Desktop and Cursor configs (via internal/clientcfg),
shows the discovered servers, asks per-client whether to import.
Writes a fresh ~/.mcp-gateway/config.jsonc with daemon defaults.
Then asks per-client whether to patch the original to point at the
gateway, then offers to install the launchd auto-start service.
Refuses to overwrite an existing non-empty config without --force.
Fully scriptable via -y or the per-step --no-* flags.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4 — Goreleaser + GitHub Actions release workflow

**Phase deliverable:** push a `v*.*.*` tag → GitHub Actions builds darwin arm64+amd64 + linux arm64+amd64 binaries, uploads them as a GitHub Release with checksums and `install.sh`, and bumps the Homebrew formula on the existing tap repo. Local snapshot dry-run reproduces the artifact set without a tag.

### Task 4.1: `.goreleaser.yaml`

**Files:**
- Create: `.goreleaser.yaml`

- [ ] **Step 1: Write the file**

`.goreleaser.yaml`:

```yaml
version: 2

project_name: mcp-gateway

before:
  hooks:
    - go mod tidy

builds:
  - id: mcp-gateway
    main: ./cmd/mcp-gateway
    binary: mcp-gateway
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.version={{.Version}} -X main.commit={{.ShortCommit}} -X main.date={{.Date}}
    goos: [darwin, linux]
    goarch: [amd64, arm64]

  - id: mgw-smoke
    main: ./cmd/mgw-smoke
    binary: mgw-smoke
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
    goos: [darwin, linux]
    goarch: [amd64, arm64]

archives:
  - id: default
    ids:
      - mcp-gateway
      - mgw-smoke
    name_template: >-
      mcp-gateway-
      {{- if eq .Os "darwin" }}macos{{ else }}{{ .Os }}{{ end -}}
      -{{ if eq .Arch "amd64" }}intel{{ else }}{{ .Arch }}{{ end }}-
      v{{ .Version }}
    formats: [tar.gz]
    files:
      - LICENSE
      - README.md

checksum:
  name_template: "mcp-gateway_v{{ .Version }}_checksums.txt"
  algorithm: sha256

snapshot:
  name_template: "{{ incpatch .Version }}-snapshot"

release:
  github:
    owner: ayu5h-raj
    name: mcp-gateway
  draft: false
  prerelease: auto
  name_template: "v{{ .Version }}"
  extra_files:
    - glob: ./scripts/install.sh
  header: |
    ## mcp-gateway v{{ .Version }}

    Local-first MCP aggregator — k9s for MCP.
  footer: |
    ## Install

    ```sh
    brew install ayu5h-raj/tap/mcp-gateway
    ```

    Or:
    ```sh
    curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
    ```

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^chore:"
      - "Merge pull request"

brews:
  - name: mcp-gateway
    repository:
      owner: ayu5h-raj
      name: homebrew-tap
      branch: main
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    directory: Formula
    homepage: https://github.com/ayu5h-raj/mcp-gateway
    description: Local-first MCP aggregator — k9s for MCP
    license: MIT
    install: |
      bin.install "mcp-gateway"
      bin.install "mgw-smoke"
    test: |
      assert_match "mcp-gateway", shell_output("#{bin}/mcp-gateway --version")
```

- [ ] **Step 2: Static config check**

Run: `goreleaser check`
Expected: prints `1 valid configuration file` and exits 0. (Install goreleaser first if missing: `brew install goreleaser`.)

If you see schema errors here, fix them before the snapshot — schema errors in `--snapshot` mode produce confusing partial output.

- [ ] **Step 3: Local snapshot dry-run**

Run: `HOMEBREW_TAP_TOKEN=fake-token-not-used-in-snapshot goreleaser release --snapshot --clean`
Expected: produces `dist/` with four `.tar.gz` archives, a checksums file, and a snapshot brew formula in `dist/homebrew-tap/Formula/mcp-gateway.rb`. The brew publish step is skipped in snapshot mode (the token is just to satisfy template expansion).

- [ ] **Step 4: Inspect artifacts**

Run: `ls -la dist/*.tar.gz`
Expected: four tarballs, names like `mcp-gateway-macos-arm64-v0.3.X-snapshot.tar.gz`.

Run: `tar tzf dist/mcp-gateway-macos-arm64-*.tar.gz`
Expected: contains `mcp-gateway`, `mgw-smoke`, `LICENSE`, `README.md`.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml
git commit -m "$(cat <<'EOF'
build: goreleaser config for v1.0.0 release pipeline

Builds darwin arm64+amd64 + linux arm64+amd64 binaries (mcp-gateway
and mgw-smoke). Tarballs use the homebrew-tap naming convention
(macos+intel aliasing). Brew formula auto-bumped on the existing
ayu5h-raj/homebrew-tap repo via HOMEBREW_TAP_TOKEN secret. install.sh
attached to each release.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 4.2: GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yaml`

- [ ] **Step 1: Write the workflow**

`.github/workflows/release.yaml`:

```yaml
name: release

on:
  push:
    tags:
      - "v*.*.*"

permissions:
  contents: write

jobs:
  release:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25"
          check-latest: true

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
```

`runs-on: macos-latest` because some of the tap formula tooling expects darwin; cross-compilation works fine from macOS to linux. Switch to `ubuntu-latest` if cost matters, but cgo-free Go cross-compiles cleanly either direction, so either works.

- [ ] **Step 2: Validate the YAML**

Run: `python3 -c 'import yaml,sys; yaml.safe_load(open(".github/workflows/release.yaml"))'`
Expected: no error.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yaml
git commit -m "$(cat <<'EOF'
ci: release workflow on v*.*.* tags

Triggers goreleaser via GoReleaser action; HOMEBREW_TAP_TOKEN secret
must be configured on the repo for the brew formula auto-bump.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5 — Homebrew formula (separate repo)

**Phase deliverable:** `Formula/mcp-gateway.rb` exists in `github.com/ayu5h-raj/homebrew-tap`, hand-authored with a placeholder version string, ready to be auto-overwritten by goreleaser on the first real release. After v1.0.0 ships, `brew install ayu5h-raj/tap/mcp-gateway` works.

### Task 5.1: Write the formula

**Files:**
- Create: `~/Documents/github/homebrew-tap/Formula/mcp-gateway.rb`

- [ ] **Step 1: Write the formula**

`~/Documents/github/homebrew-tap/Formula/mcp-gateway.rb`:

```ruby
class McpGateway < Formula
  desc "Local-first MCP aggregator — k9s for MCP"
  homepage "https://github.com/ayu5h-raj/mcp-gateway"
  version "1.0.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-macos-arm64-v1.0.0.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-macos-intel-v1.0.0.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-linux-arm64-v1.0.0.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-linux-intel-v1.0.0.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    bin.install "mcp-gateway"
    bin.install "mgw-smoke"
  end

  test do
    assert_match "mcp-gateway", shell_output("#{bin}/mcp-gateway --version")
  end
end
```

The placeholder SHAs will be overwritten by goreleaser's brew action on the first real release. Committing the placeholder ensures the file exists in the tap so a `brew tap-new`-style bootstrap isn't needed.

- [ ] **Step 2: Commit and push the tap**

```bash
cd ~/Documents/github/homebrew-tap
git add Formula/mcp-gateway.rb
git commit -m "feat: add mcp-gateway formula (placeholder pending v1.0.0 release)"
git push
cd ~/Documents/github/mcp-gateway
```

- [ ] **Step 3: Verify tap is reachable**

Run: `git ls-remote git@github.com:ayu5h-raj/homebrew-tap.git Formula/mcp-gateway.rb` (or check the GitHub web UI).
Expected: file present on `main`.

---

## Phase 6 — `install.sh`

**Phase deliverable:** `scripts/install.sh` is a POSIX-sh script that detects platform, downloads the matching tarball from the latest GitHub Release (or a pinned `MCP_GATEWAY_VERSION`), verifies SHA256, and installs both binaries to `/usr/local/bin` (with `~/.local/bin` fallback if not writable).

### Task 6.1: Write `scripts/install.sh`

**Files:**
- Create: `scripts/install.sh`

- [ ] **Step 1: Write the script**

`scripts/install.sh`:

```sh
#!/bin/sh
# install.sh — install mcp-gateway from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
#   MCP_GATEWAY_VERSION=v1.0.0 sh install.sh
#   MCP_GATEWAY_PREFIX=$HOME/.local/bin sh install.sh

set -eu

OWNER="ayu5h-raj"
REPO="mcp-gateway"
BIN_NAMES="mcp-gateway mgw-smoke"

err() { printf "install.sh: %s\n" "$*" >&2; exit 1; }
info() { printf "  %s\n" "$*"; }

need() {
  command -v "$1" >/dev/null 2>&1 || err "missing required tool: $1"
}

need curl
need tar
need uname

# Detect OS.
case "$(uname -s)" in
  Darwin) OS=macos ;;
  Linux)  OS=linux ;;
  *)      err "unsupported OS: $(uname -s)" ;;
esac

# Detect arch.
case "$(uname -m)" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64)  ARCH=intel ;;
  *)             err "unsupported arch: $(uname -m)" ;;
esac

# Resolve version (default: latest release tag from GitHub API).
if [ -z "${MCP_GATEWAY_VERSION:-}" ]; then
  info "Resolving latest release..."
  VERSION=$(
    curl -fsSL "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
      | grep '"tag_name":' \
      | head -n 1 \
      | sed 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/'
  )
  [ -n "$VERSION" ] || err "could not resolve latest release; set MCP_GATEWAY_VERSION explicitly"
else
  VERSION="$MCP_GATEWAY_VERSION"
fi

case "$VERSION" in
  v*) VERSION_NO_V=$(echo "$VERSION" | sed 's/^v//') ;;
  *)  err "VERSION must start with v (got: $VERSION)" ;;
esac

ARCHIVE="mcp-gateway-${OS}-${ARCH}-v${VERSION_NO_V}.tar.gz"
CHECKSUMS="mcp-gateway_v${VERSION_NO_V}_checksums.txt"
BASE_URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}"

info "Installing mcp-gateway ${VERSION} for ${OS}/${ARCH}..."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

info "Downloading $ARCHIVE..."
curl -fsSL -o "$TMP/$ARCHIVE" "$BASE_URL/$ARCHIVE" \
  || err "download failed: $BASE_URL/$ARCHIVE"

info "Downloading $CHECKSUMS..."
curl -fsSL -o "$TMP/$CHECKSUMS" "$BASE_URL/$CHECKSUMS" \
  || err "checksums download failed"

info "Verifying SHA256..."
EXPECTED=$(grep "$ARCHIVE" "$TMP/$CHECKSUMS" | awk '{print $1}')
[ -n "$EXPECTED" ] || err "no checksum for $ARCHIVE in $CHECKSUMS"

if command -v shasum >/dev/null 2>&1; then
  ACTUAL=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
elif command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
else
  err "neither shasum nor sha256sum is available"
fi
[ "$EXPECTED" = "$ACTUAL" ] || err "checksum mismatch (expected $EXPECTED, got $ACTUAL)"

info "Extracting..."
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

# Pick install prefix.
PREFIX="${MCP_GATEWAY_PREFIX:-/usr/local/bin}"
if [ ! -w "$PREFIX" ] && [ "$(id -u)" -ne 0 ]; then
  USE_SUDO=1
else
  USE_SUDO=0
fi

# Fallback to ~/.local/bin if /usr/local/bin not writable and sudo not available.
if [ "$USE_SUDO" = "1" ] && ! command -v sudo >/dev/null 2>&1; then
  PREFIX="$HOME/.local/bin"
  USE_SUDO=0
  mkdir -p "$PREFIX"
  info "(no sudo, falling back to $PREFIX — make sure it's on your PATH)"
fi

for bin in $BIN_NAMES; do
  if [ -f "$TMP/$bin" ]; then
    if [ "$USE_SUDO" = "1" ]; then
      sudo install -m 0755 "$TMP/$bin" "$PREFIX/$bin"
    else
      install -m 0755 "$TMP/$bin" "$PREFIX/$bin"
    fi
    info "Installed $PREFIX/$bin"
  fi
done

cat <<EOF

✓ mcp-gateway ${VERSION} installed.

Next steps:
  mcp-gateway init        # first-run wizard
  mcp-gateway --help      # see all subcommands

EOF
```

- [ ] **Step 2: Parse-check**

Run: `sh -n scripts/install.sh`
Expected: no output (clean parse).

- [ ] **Step 3: shellcheck (optional but useful)**

Run: `command -v shellcheck >/dev/null && shellcheck scripts/install.sh || echo "shellcheck not installed, skipping"`
Expected: no findings (or shellcheck not installed).

- [ ] **Step 4: chmod +x**

Run: `chmod +x scripts/install.sh`
Expected: silent.

- [ ] **Step 5: Commit**

```bash
git add scripts/install.sh
git commit -m "$(cat <<'EOF'
feat(install): scripts/install.sh — POSIX-sh installer for non-brew users

Detects OS+arch, fetches the matching tarball from GitHub Releases (or
MCP_GATEWAY_VERSION env), verifies SHA256, installs to /usr/local/bin
with ~/.local/bin fallback. Both mcp-gateway and mgw-smoke binaries.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 6.2: CI parse-check

**Files:**
- Modify: `.github/workflows/ci.yaml` (assuming it exists; if not, create it)

- [ ] **Step 1: Inspect existing CI**

Run: `cat .github/workflows/ci.yaml 2>/dev/null || echo "no ci.yaml"`
Expected: either prints the existing CI or "no ci.yaml".

- [ ] **Step 2: Add a step (if ci.yaml exists, append; otherwise skip — release.yaml already covers releases)**

If a CI workflow exists, add a step in the test job:

```yaml
      - name: Validate install.sh
        run: sh -n scripts/install.sh
```

If you need to create `.github/workflows/ci.yaml` from scratch, defer that to a separate PR — Plan 04 doesn't require it.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yaml 2>/dev/null
git commit -m "ci: validate install.sh syntax on every push" 2>/dev/null || true
```

(Skip if no ci.yaml change was needed.)

---

## Phase 7 — README + uninstall + runbook

**Phase deliverable:** README.md leads with the v1.0 install one-liner. New `docs/release-runbook.md` documents how to cut a release and rotate the HOMEBREW_TAP_TOKEN. CHANGELOG.md exists with the v1.0.0 entry stub.

### Task 7.1: Rewrite Quick Start

**Files:**
- Modify: `README.md` (top section)

- [ ] **Step 1: Read the current README to know what to preserve**

Run: `head -80 README.md`
Expected: shows the current intro and Quick Start.

- [ ] **Step 2: Replace the Quick Start section**

Find the "## Quick Start" or equivalent section in README.md and replace with:

```markdown
## Install

**Homebrew (recommended on macOS):**

```sh
brew install ayu5h-raj/tap/mcp-gateway
```

**One-liner installer (Linux + macOS):**

```sh
curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
```

**From source:**

```sh
git clone https://github.com/ayu5h-raj/mcp-gateway && cd mcp-gateway && make build
```

## Quick Start

```sh
mcp-gateway init
```

`init` detects existing MCP client configs (Claude Desktop, Cursor), offers to migrate their servers into one mcp-gateway config, optionally patches each client to point at the gateway, and optionally installs a launchd auto-start service. Restart your MCP client and you're done.

For non-interactive (e.g. dotfile bootstrap):

```sh
mcp-gateway init -y
```

## Daily use

```sh
mcp-gateway tui              # live ops dashboard
mcp-gateway list             # show servers and their states
mcp-gateway add <name> --command npx --arg -y --arg @scope/server
mcp-gateway disable <name>   # stop a server without removing it
mcp-gateway service status   # is the launchd service running?
```
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): v1.0 install + Quick Start rewrite"
```

### Task 7.2: Uninstall + service sections

**Files:**
- Modify: `README.md` (append new sections near the bottom)

- [ ] **Step 1: Append**

```markdown
## Uninstall

```sh
mcp-gateway service uninstall      # remove the launchd plist
brew uninstall mcp-gateway          # or: rm /usr/local/bin/mcp-gateway /usr/local/bin/mgw-smoke
rm -rf ~/.mcp-gateway               # config + logs + pidfile
```

Each step is idempotent. `service uninstall` on a never-installed system is a no-op.

## Service management (macOS)

mcp-gateway can install a launchd plist so the daemon auto-starts on login and respawns if it crashes.

```sh
mcp-gateway service install     # generate plist + launchctl bootstrap
mcp-gateway service status      # show installed / loaded / pid
mcp-gateway service uninstall   # bootout + remove plist
```

The plist label is `com.ayu5h-raj.mcp-gateway`. Logs go to `~/.mcp-gateway/daemon.log`. Edit the plist directly if you need a custom PATH or working dir; reinstall to pick up changes.

Linux: deferred to v1.1. For now, run `mcp-gateway start` from your shell rc.

## Troubleshooting

**macOS Gatekeeper warning** ("cannot be opened because the developer cannot be verified"). Affects only the `install.sh` path; `brew` users are unaffected. Fix:

```sh
xattr -d com.apple.quarantine /usr/local/bin/mcp-gateway /usr/local/bin/mgw-smoke
```

**Daemon log:** `~/.mcp-gateway/daemon.log` (or whatever `StandardOutPath` your plist points at).

**File an issue:** https://github.com/ayu5h-raj/mcp-gateway/issues
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(readme): uninstall, service management, troubleshooting"
```

### Task 7.3: Release runbook

**Files:**
- Create: `docs/release-runbook.md`

- [ ] **Step 1: Write the runbook**

```markdown
# Release Runbook

## Cutting a release

```sh
# 1. Update CHANGELOG.md with the new version section.
# 2. Tag and push.
git tag -a v1.x.y -m "v1.x.y"
git push origin v1.x.y
# 3. Watch the release workflow.
gh run watch  # or check the Actions tab
```

The release workflow (`.github/workflows/release.yaml`) does:
- Builds darwin arm64+amd64 + linux arm64+amd64 binaries.
- Creates a GitHub Release with archives, checksums, and `install.sh`.
- Pushes an updated `Formula/mcp-gateway.rb` to `ayu5h-raj/homebrew-tap`.

## Verifying after release

```sh
brew untap ayu5h-raj/tap 2>/dev/null
brew install ayu5h-raj/tap/mcp-gateway
mcp-gateway --version    # matches the tag
mcp-gateway init -y      # smoke
```

Then:

```sh
brew uninstall mcp-gateway
curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
mcp-gateway --version
```

## Rotating HOMEBREW_TAP_TOKEN

The `HOMEBREW_TAP_TOKEN` is a fine-grained GitHub PAT with `contents:write` on the `homebrew-tap` repo only. It expires; rotate annually or on any suspicion of compromise.

1. Go to https://github.com/settings/personal-access-tokens
2. Create a new fine-grained token. Scope: only `ayu5h-raj/homebrew-tap`. Permission: `Contents: read & write`. Expiry: 1 year.
3. Copy the token.
4. In `ayu5h-raj/mcp-gateway` → Settings → Secrets and variables → Actions → update `HOMEBREW_TAP_TOKEN`.
5. Revoke the old token.

## Hotfix release

For a fix that needs to ship out-of-cycle:

```sh
git checkout main
git pull
# fix
git commit -am "fix: ..."
git push
git tag -a v1.x.(y+1) -m "v1.x.(y+1)"
git push origin v1.x.(y+1)
```

Same workflow runs.
```

- [ ] **Step 2: Commit**

```bash
git add docs/release-runbook.md
git commit -m "docs: release runbook (cut release, rotate tap token, hotfix)"
```

### Task 7.4: CHANGELOG.md

**Files:**
- Create: `CHANGELOG.md`

- [ ] **Step 1: Write the file**

```markdown
# Changelog

All notable changes to mcp-gateway are documented here. Versions follow [SemVer](https://semver.org/).

## [Unreleased]

(Nothing here yet.)

## [1.0.0] — 2026-04-XX

### Added
- `mcp-gateway init` — first-run wizard. Detects Claude Desktop and Cursor configs, migrates servers, patches the client to point at the gateway, optionally installs a launchd auto-start service.
- `mcp-gateway service install | uninstall | status` — manages the macOS launchd plist that auto-starts the daemon on login.
- Goreleaser-driven release pipeline. Binaries for darwin arm64+amd64 and linux arm64+amd64 published to GitHub Releases.
- Homebrew formula in the existing `ayu5h-raj/homebrew-tap`. Install via `brew install ayu5h-raj/tap/mcp-gateway`.
- POSIX-sh `install.sh` for non-brew installs (Linux + macOS).
- `docs/release-runbook.md` — how to cut releases and rotate the tap token.

### Changed
- README.md rewritten around the new install one-liner and `init` flow.

### Deferred
- Linux systemd unit (Linux v1.0 users run `mcp-gateway start` from their shell rc).
- macOS code signing / notarization (`install.sh` users see one Gatekeeper warning; documented workaround in README).
- In-binary auto-updater. Use `brew upgrade` or re-run `install.sh`.
- Windows support.

## [0.3.x] — 2026-04-24

- TUI (`mcp-gateway tui`) — three tabs: Servers, Events, Tools. Read-only ops surface.

## [0.2.x] — 2026-04-24

- Pidfile, event bus, admin RPC over UNIX socket.
- Mutation CLI: `add`, `rm`, `enable`, `disable`, `list`, `start`, `stop`, `restart`.
- `${env:NAME}` reference resolver in config.

## [0.1.x] — 2026-04-23

- First public alpha. Daemon, supervisor, aggregator, mcpchild, stdio bridge.
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: add CHANGELOG.md with v1.0.0 entry"
```

---

## Phase 8 — Release dry run, then v1.0.0

**Phase deliverable:** `v1.0.0` is tagged and the release workflow has produced a clean GitHub Release with archives + checksums + install.sh, and the brew tap formula has been auto-bumped. `brew install ayu5h-raj/tap/mcp-gateway` works end-to-end on a fresh machine.

### Task 8.1: Verify the repo is clean and CI is green

- [ ] **Step 1: Working tree status**

Run: `git status`
Expected: clean, on main, up to date with origin.

- [ ] **Step 2: Run the full local check**

Run: `make test && make e2e && go vet ./... && make lint`
Expected: all green.

- [ ] **Step 3: Verify CI is green on main**

Run: `gh run list --branch main --limit 5` (or check Actions tab).
Expected: latest run on main is green.

### Task 8.2: Release candidate dry run

- [ ] **Step 1: Tag the rc**

Run: `git tag -a v1.0.0-rc1 -m "v1.0.0-rc1"`
Expected: silent.

- [ ] **Step 2: Push the tag**

Run: `git push origin v1.0.0-rc1`
Expected: triggers the release workflow.

- [ ] **Step 3: Watch the workflow**

Run: `gh run watch --exit-status` (or check Actions tab).
Expected: workflow completes green. Inspect the GitHub Release for:
- Four `.tar.gz` archives (one per OS+arch)
- A `mcp-gateway_v1.0.0-rc1_checksums.txt`
- `install.sh` attached
- The brew tap repo has been updated with new SHAs

If the brew tap update fails, the most likely cause is `HOMEBREW_TAP_TOKEN` permissions — verify per `docs/release-runbook.md`.

### Task 8.3: Smoke test the rc end-to-end

- [ ] **Step 1: Brew install from rc**

Run: `brew uninstall mcp-gateway 2>/dev/null; brew install ayu5h-raj/tap/mcp-gateway`
Expected: installs the rc1 build.

- [ ] **Step 2: Test init in a clean home**

Run:
```sh
TMPHOME=$(mktemp -d)
HOME="$TMPHOME" mcp-gateway init -y --no-patch --no-service
HOME="$TMPHOME" mcp-gateway start
sleep 2
HOME="$TMPHOME" mcp-gateway status
HOME="$TMPHOME" mcp-gateway stop
rm -rf "$TMPHOME"
```
Expected: `init` writes config, `start` brings the daemon up, `status` reports OK, `stop` shuts down.

- [ ] **Step 3: Test install.sh in a Docker container**

Run:
```sh
docker run --rm -t alpine:latest sh -c '
  apk add --no-cache curl bash
  MCP_GATEWAY_VERSION=v1.0.0-rc1 sh -c "$(curl -fsSL https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0-rc1/install.sh)"
  /usr/local/bin/mcp-gateway --version
'
```
Expected: prints the version. (Linux arm64 if you're on Apple Silicon — that's fine, it's still a real test.)

If anything breaks: fix on `main`, push, tag `v1.0.0-rc2`, repeat.

### Task 8.4: Cut v1.0.0

- [ ] **Step 1: Update CHANGELOG**

Edit `CHANGELOG.md` — replace `[1.0.0] — 2026-04-XX` with the actual date. Commit:

```bash
git add CHANGELOG.md
git commit -m "docs: pin v1.0.0 release date"
git push
```

- [ ] **Step 2: Tag and push**

```bash
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

- [ ] **Step 3: Watch the release workflow**

Run: `gh run watch --exit-status`
Expected: green. New GitHub Release at `v1.0.0`. Tap formula updated with v1.0.0 SHAs.

- [ ] **Step 4: Final smoke**

```sh
brew uninstall mcp-gateway
brew update
brew install ayu5h-raj/tap/mcp-gateway
mcp-gateway --version    # 1.0.0
mcp-gateway init -y      # full path with import + patch + service install
mcp-gateway service status
```

If clean: 🎉 v1.0.0 shipped.

If not clean: roll back the brew tap commit, fix on main, tag `v1.0.1`.

### Task 8.5: Announce (optional)

- [ ] Post a release note (Twitter/X, /r/ClaudeAI, etc.) linking the GitHub Release.
- [ ] Add a "What's new in v1.0" section to the project's website if there is one (there isn't yet — out of scope).

---

## Self-review checklist (run before declaring Plan 04 complete)

- [ ] All eight phases shipped.
- [ ] `git tag -l v1.0.0` exists and the GitHub Release page is populated.
- [ ] `brew install ayu5h-raj/tap/mcp-gateway` works on a fresh machine.
- [ ] `curl -fsSL .../install.sh | sh` works on a fresh Linux container.
- [ ] `mcp-gateway init -y` on a fresh `HOME` produces a working install with no manual steps.
- [ ] `mcp-gateway service status` on a fresh install reports `not installed`.
- [ ] `mcp-gateway service install && mcp-gateway service status` reports `installed + loaded + pid`.
- [ ] CHANGELOG.md, README.md, docs/release-runbook.md all up to date.
- [ ] No regressions in `make test`, `make e2e`, `go vet ./...`, `make lint`.
