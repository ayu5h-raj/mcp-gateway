# mcp-gateway — Plan 01: Foundation + Working Daemon

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a v0.1 mcp-gateway daemon in Go that aggregates N stdio-based downstream MCP servers behind one Streamable HTTP endpoint on `127.0.0.1`, plus a `stdio` bridge subcommand for Claude Desktop. No TUI, no secrets CLI, no packaging yet — those are Plan 02 and Plan 03.

**Architecture:** Cobra-dispatched Go binary. `daemon` subcommand runs a long-lived process that spawns and supervises N stdio children (via `os/exec` with `Setpgid`), talks to each as an MCP client (using the official `modelcontextprotocol/go-sdk`), and exposes a merged MCP endpoint (Streamable HTTP) to upstream clients. `stdio` subcommand is a thin bridge that proxies MCP frames between stdin/stdout and the daemon's HTTP endpoint. Config is a single JSONC file, hot-reloaded via fsnotify.

**Tech Stack:** Go 1.23+, Cobra (CLI), `modelcontextprotocol/go-sdk` (MCP protocol), fsnotify (hot reload), stdlib `log/slog` (structured logging), stdlib `net/http`/`encoding/json`, `tidwall/jsonc` or inline preprocessor, testify for test assertions, `golangci-lint` for linting, `goreleaser` (deferred to Plan 03).

**Reference spec:** `docs/superpowers/specs/2026-04-23-mcp-gateway-design.md`.

**v0.1 Success criterion:** A user writes a `config.jsonc` with one downstream MCP server (e.g. filesystem), runs `mcp-gateway daemon`, points Claude Desktop at `mcp-gateway stdio`, and successfully lists and calls tools end-to-end. Changing the config file (adding/removing/editing a server) reconciles without a daemon restart.

**What this plan does NOT cover (deferred to later plans):**
- TUI (Plan 02)
- Event bus + token estimator (Plan 02 — needed by TUI)
- Secret resolver + `secret` CLI (Plan 02)
- Admin RPC + UNIX socket listener (Plan 02 — TUI depends on it)
- CLI mutation commands (`add`, `rm`, `enable`, `disable`) (Plan 02)
- First-run wizard (Plan 02)
- Pidfile + service/launchd (Plan 02)
- Packaging / release / brew tap (Plan 03)
- OAuth, sampling, elicitation forwarding (post-v0)
- HTTP/SSE downstream servers (post-v0)

---

## File Structure (this plan creates)

```
mcp-gateway/
├── cmd/mcp-gateway/
│   └── main.go                        # Cobra root + subcommand dispatch
├── internal/
│   ├── config/
│   │   ├── config.go                  # Config + Server types
│   │   ├── parse.go                   # JSONC preprocessor + parse
│   │   ├── parse_test.go
│   │   ├── validate.go                # Invariant checks
│   │   ├── validate_test.go
│   │   ├── watcher.go                 # fsnotify wrapper w/ debounce
│   │   └── watcher_test.go
│   ├── supervisor/
│   │   ├── server.go                  # Server state + state machine
│   │   ├── server_test.go
│   │   ├── process.go                 # Spawn subprocess (exec + setpgid)
│   │   ├── process_test.go
│   │   ├── supervisor.go              # Orchestrates N Servers
│   │   └── supervisor_test.go
│   ├── mcpchild/
│   │   ├── client.go                  # MCP client wrapped around a supervised child
│   │   └── client_test.go
│   ├── aggregator/
│   │   ├── aggregator.go              # Merge tools/resources/prompts
│   │   ├── aggregator_test.go
│   │   ├── prefix.go                  # Prefix / unprefix tool and resource names
│   │   └── prefix_test.go
│   ├── router/
│   │   ├── router.go                  # Routes tools/call, resources/read, etc.
│   │   └── router_test.go
│   ├── daemon/
│   │   ├── daemon.go                  # Top-level daemon lifecycle
│   │   ├── http.go                    # Streamable HTTP server on 127.0.0.1
│   │   ├── session.go                 # Upstream session registry
│   │   └── daemon_test.go
│   ├── bridge/
│   │   ├── bridge.go                  # stdio ↔ HTTP bridge
│   │   └── bridge_test.go
│   └── testutil/
│       └── fakechild/
│           └── fakechild.go           # A fake stdio MCP server binary for tests
├── schema/
│   └── config.schema.json             # JSON Schema for config (future IDE support)
├── Makefile
├── .golangci.yml
├── .github/workflows/ci.yml
├── .gitignore
├── LICENSE
├── go.mod
└── go.sum
```

---

## Phase 0 — Scaffolding

### Task 0.1: Initialize Go module + gitignore

**Files:**
- Create: `go.mod`
- Create: `.gitignore`

- [ ] **Step 1:** Run `go mod init github.com/ayushraj/mcp-gateway` at the repo root.

```bash
go mod init github.com/ayushraj/mcp-gateway
```

Expected output:

```
go: creating new go.mod: module github.com/ayushraj/mcp-gateway
```

- [ ] **Step 2:** Create `.gitignore`:

```
# Binaries
bin/
/mcp-gateway

# Test output
*.out
coverage.txt
coverage.html

# IDE
.idea/
.vscode/
*.swp
.DS_Store

# Build artifacts
dist/

# Local state
.env
.env.local
```

- [ ] **Step 3:** Verify `go.mod` exists and matches:

```bash
cat go.mod
```

Expected:

```
module github.com/ayushraj/mcp-gateway

go 1.23
```

- [ ] **Step 4:** Commit:

```bash
git add go.mod .gitignore
git commit -m "chore: initialize Go module"
```

### Task 0.2: Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1:** Create `Makefile` with the following exact content:

```makefile
.PHONY: build test lint vet fmt tidy clean e2e

BINARY := mcp-gateway
PKG := github.com/ayushraj/mcp-gateway
BIN_DIR := bin

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

install: build
	install -m 0755 $(BIN_DIR)/$(BINARY) $${GOBIN:-$${GOPATH:-$$HOME/go}/bin}/$(BINARY)

test:
	go test -race -count=1 ./...

cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "install golangci-lint: https://golangci-lint.run"; exit 1; }
	golangci-lint run ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .
	go mod tidy

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

e2e:
	go test -race -count=1 -tags=e2e ./internal/daemon/...
```

- [ ] **Step 2:** Verify `make build` reports nothing to build yet (no Go files):

```bash
make build
```

Expected: an error like `no Go files in ./cmd/mcp-gateway` — that's fine, we haven't written anything yet.

- [ ] **Step 3:** Commit:

```bash
git add Makefile
git commit -m "chore: add Makefile"
```

### Task 0.3: golangci-lint config

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1:** Create `.golangci.yml`:

```yaml
run:
  timeout: 3m
  go: "1.23"

linters:
  disable-all: true
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gofmt
    - goimports
    - gocritic
    - revive
    - misspell

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
  exclude-rules:
    - path: _test\.go
      linters:
        - errcheck
        - gocritic
```

- [ ] **Step 2:** Commit:

```bash
git add .golangci.yml
git commit -m "chore: add golangci-lint config"
```

### Task 0.4: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1:** Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  pull_request:
  push:
    branches: [main]

jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
          cache: true

      - name: Verify modules
        run: go mod download

      - name: Vet
        run: go vet ./...

      - name: Lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest
          args: --timeout=3m

      - name: Test
        run: go test -race -count=1 ./...

      - name: Build
        run: |
          mkdir -p bin
          go build -o bin/mcp-gateway ./cmd/mcp-gateway
```

- [ ] **Step 2:** Commit:

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add lint + test + build workflow"
```

### Task 0.5: LICENSE

**Files:**
- Create: `LICENSE`

- [ ] **Step 1:** Create `LICENSE` with the MIT license text. Replace `YYYY` with `2026` and `YOUR NAME` with the user's preferred name (default: `Ayush Raj`):

```
MIT License

Copyright (c) 2026 Ayush Raj

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

- [ ] **Step 2:** Commit:

```bash
git add LICENSE
git commit -m "chore: add MIT license"
```

### Task 0.6: Add core dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1:** Add runtime and test dependencies:

```bash
go get github.com/spf13/cobra@latest
go get github.com/fsnotify/fsnotify@latest
go get github.com/tidwall/jsonc@latest
go get github.com/modelcontextprotocol/go-sdk@latest
go get github.com/stretchr/testify@latest
go mod tidy
```

Expected: `go.mod` updated with the five modules, `go.sum` populated.

> **Note:** If `github.com/modelcontextprotocol/go-sdk` fails to resolve, fall back to `github.com/mark3labs/mcp-go@latest` and adjust imports in later tasks accordingly. Record the choice in a `docs/implementation-notes.md` (create if absent).

- [ ] **Step 2:** Verify:

```bash
go mod tidy && cat go.mod | grep -E "cobra|fsnotify|jsonc|mcp|testify"
```

Expected: 5 `require` lines visible.

- [ ] **Step 3:** Commit:

```bash
git add go.mod go.sum
git commit -m "chore: add core dependencies"
```

### Task 0.7: Cobra skeleton

**Files:**
- Create: `cmd/mcp-gateway/main.go`

- [ ] **Step 1:** Create `cmd/mcp-gateway/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags.
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mcp-gateway",
		Short:         "Local-first MCP aggregator daemon",
		Long:          "mcp-gateway aggregates multiple MCP servers behind one endpoint.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newStdioCmd())
	root.AddCommand(newStatusCmd())
	return root
}

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the mcp-gateway daemon (long-running)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("daemon: not yet implemented")
		},
	}
}

func newStdioCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stdio",
		Short: "Run as a stdio bridge to the local daemon (spawn target for stdio-only clients)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("stdio: not yet implemented")
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("status: not yet implemented")
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2:** Build:

```bash
make build
```

Expected: no errors, `bin/mcp-gateway` exists.

- [ ] **Step 3:** Smoke test:

```bash
./bin/mcp-gateway --help
```

Expected: help output listing `daemon`, `stdio`, `status` subcommands.

- [ ] **Step 4:** Commit:

```bash
git add cmd/mcp-gateway/main.go
git commit -m "feat: add Cobra CLI skeleton with daemon/stdio/status stubs"
```

---

## Phase 1 — Config parsing

### Task 1.1: Config types

**Files:**
- Create: `internal/config/config.go`

- [ ] **Step 1:** Create `internal/config/config.go`:

```go
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Version is the currently-supported schema version.
const Version = 1

// Config is the top-level user-facing configuration.
type Config struct {
	Schema     string            `json:"$schema,omitempty"`
	Version    int               `json:"version"`
	Daemon     Daemon            `json:"daemon"`
	McpServers map[string]Server `json:"mcpServers"`
}

// Daemon groups daemon-scoped settings.
type Daemon struct {
	HTTPPort                      int    `json:"http_port"`
	LogLevel                      string `json:"log_level"`
	EventBufferSize               int    `json:"event_buffer_size,omitempty"`
	ChildRestartBackoffMaxSeconds int    `json:"child_restart_backoff_max_seconds,omitempty"`
	ChildRestartMaxAttempts       int    `json:"child_restart_max_attempts,omitempty"`
}

// Server is one downstream MCP server definition.
type Server struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	Prefix  string            `json:"prefix,omitempty"`
}

// DefaultDaemon returns the daemon defaults applied when config omits fields.
func DefaultDaemon() Daemon {
	return Daemon{
		HTTPPort:                      7823,
		LogLevel:                      "info",
		EventBufferSize:               10000,
		ChildRestartBackoffMaxSeconds: 60,
		ChildRestartMaxAttempts:       5,
	}
}

// EffectivePrefix returns the tool/resource prefix for a server.
// Defaults to the server key (map name) if unset.
func EffectivePrefix(name string, s Server) string {
	if strings.TrimSpace(s.Prefix) != "" {
		return s.Prefix
	}
	return name
}

// DefaultConfigPath returns the default on-disk location for the user's config.
func DefaultConfigPath(home string) string {
	return filepath.Join(home, ".mcp-gateway", "config.jsonc")
}

// FormatError wraps a parse/validate error with the originating file.
type FormatError struct {
	Path string
	Err  error
}

func (e *FormatError) Error() string {
	if e.Path == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Err.Error())
}

func (e *FormatError) Unwrap() error { return e.Err }
```

- [ ] **Step 2:** Verify it compiles:

```bash
go build ./internal/config/...
```

Expected: no errors.

- [ ] **Step 3:** Commit:

```bash
git add internal/config/config.go
git commit -m "feat(config): add Config/Server/Daemon types and defaults"
```

### Task 1.2: JSONC preprocessor + parser — write the test first

**Files:**
- Create: `internal/config/parse_test.go`

- [ ] **Step 1:** Create `internal/config/parse_test.go`:

```go
package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_MinimalValid(t *testing.T) {
	in := `{
	  "version": 1,
	  "daemon": { "http_port": 7823, "log_level": "info" },
	  "mcpServers": {
	    "github": {
	      "command": "npx",
	      "args": ["-y", "@modelcontextprotocol/server-github"],
	      "enabled": true
	    }
	  }
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, 1, c.Version)
	assert.Equal(t, 7823, c.Daemon.HTTPPort)
	assert.Equal(t, "info", c.Daemon.LogLevel)
	require.Contains(t, c.McpServers, "github")
	gh := c.McpServers["github"]
	assert.Equal(t, "npx", gh.Command)
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-github"}, gh.Args)
	assert.True(t, gh.Enabled)
}

func TestParse_StripsLineAndBlockComments(t *testing.T) {
	in := `{
	  // top-level comment
	  "version": 1, /* inline block */
	  "daemon": { "http_port": 7823, "log_level": "info" },
	  "mcpServers": {
	    // no servers configured yet
	  }
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, 1, c.Version)
	assert.Empty(t, c.McpServers)
}

func TestParse_TolerantOfTrailingCommas(t *testing.T) {
	in := `{
	  "version": 1,
	  "daemon": { "http_port": 7823, "log_level": "info", },
	  "mcpServers": { "fs": { "command": "cat", "enabled": true, }, }
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.True(t, c.McpServers["fs"].Enabled)
}

func TestParse_AppliesDefaults(t *testing.T) {
	in := `{
	  "version": 1,
	  "daemon": {},
	  "mcpServers": {}
	}`
	c, err := Parse(strings.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, 7823, c.Daemon.HTTPPort)
	assert.Equal(t, "info", c.Daemon.LogLevel)
	assert.Equal(t, 10000, c.Daemon.EventBufferSize)
	assert.Equal(t, 60, c.Daemon.ChildRestartBackoffMaxSeconds)
	assert.Equal(t, 5, c.Daemon.ChildRestartMaxAttempts)
}

func TestParse_ReturnsErrorOnMalformedJSON(t *testing.T) {
	in := `{"version": 1, "daemon": { "http_port": 7823,`
	_, err := Parse(strings.NewReader(in))
	require.Error(t, err)
}
```

- [ ] **Step 2:** Run tests — expect compile error ("undefined: Parse"):

```bash
go test ./internal/config/ -run TestParse -v
```

Expected: build fails; `Parse` is undefined. Good.

### Task 1.3: JSONC preprocessor + parser — implement

**Files:**
- Create: `internal/config/parse.go`

- [ ] **Step 1:** Create `internal/config/parse.go`:

```go
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tidwall/jsonc"
)

// Parse reads config bytes (JSONC) from r and returns a fully-defaulted Config.
// Errors are wrapped in FormatError (the caller may Unwrap for the raw cause).
func Parse(r io.Reader) (*Config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, &FormatError{Err: fmt.Errorf("read: %w", err)}
	}
	// Strip JSONC comments and trailing commas → canonical JSON.
	pure := jsonc.ToJSON(raw)
	// Default values first, then overlay what the user set.
	c := &Config{
		Version:    Version,
		Daemon:     DefaultDaemon(),
		McpServers: map[string]Server{},
	}
	dec := json.NewDecoder(bytes.NewReader(pure))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, &FormatError{Err: fmt.Errorf("decode: %w", err)}
	}
	// Reapply daemon defaults for any zero-valued fields the user omitted
	// (json.Decode overwrites our defaults with zero when fields are absent
	// in a subobject).
	d := c.Daemon
	def := DefaultDaemon()
	if d.HTTPPort == 0 {
		d.HTTPPort = def.HTTPPort
	}
	if d.LogLevel == "" {
		d.LogLevel = def.LogLevel
	}
	if d.EventBufferSize == 0 {
		d.EventBufferSize = def.EventBufferSize
	}
	if d.ChildRestartBackoffMaxSeconds == 0 {
		d.ChildRestartBackoffMaxSeconds = def.ChildRestartBackoffMaxSeconds
	}
	if d.ChildRestartMaxAttempts == 0 {
		d.ChildRestartMaxAttempts = def.ChildRestartMaxAttempts
	}
	c.Daemon = d
	return c, nil
}

// ParseFile is a convenience wrapper around Parse that opens a file by path.
func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &FormatError{Path: path, Err: err}
	}
	defer f.Close()
	c, err := Parse(f)
	if err != nil {
		if fe, ok := err.(*FormatError); ok {
			fe.Path = path
			return nil, fe
		}
		return nil, &FormatError{Path: path, Err: err}
	}
	return c, nil
}
```

- [ ] **Step 2:** Run tests — expect PASS:

```bash
go test ./internal/config/ -run TestParse -v
```

Expected: all 5 test cases pass.

> **Note:** `DisallowUnknownFields` is intentional — we want malformed configs to fail loud, not silently ignore a typo like `mcp_servers` vs `mcpServers`. If this triggers a problem with a future intentional field, we'll revisit.

- [ ] **Step 3:** Commit:

```bash
git add internal/config/parse.go internal/config/parse_test.go
git commit -m "feat(config): JSONC parser with default-fill semantics"
```

### Task 1.4: Validator — write the test first

**Files:**
- Create: `internal/config/validate_test.go`

- [ ] **Step 1:** Create `internal/config/validate_test.go`:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCfg() *Config {
	return &Config{
		Version: 1,
		Daemon:  DefaultDaemon(),
		McpServers: map[string]Server{
			"ok": {Command: "echo", Enabled: true},
		},
	}
}

func TestValidate_Minimal(t *testing.T) {
	require.NoError(t, Validate(validCfg()))
}

func TestValidate_RejectsUnsupportedVersion(t *testing.T) {
	c := validCfg()
	c.Version = 2
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestValidate_RejectsInvalidPort(t *testing.T) {
	c := validCfg()
	c.Daemon.HTTPPort = 0
	require.Error(t, Validate(c))
	c.Daemon.HTTPPort = 70000
	require.Error(t, Validate(c))
}

func TestValidate_RejectsEmptyCommand(t *testing.T) {
	c := validCfg()
	c.McpServers["bad"] = Server{Command: "", Enabled: true}
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad")
	assert.Contains(t, err.Error(), "command")
}

func TestValidate_RejectsEmptyPrefixWhenExplicit(t *testing.T) {
	c := validCfg()
	s := c.McpServers["ok"]
	// Explicit empty prefix is NOT allowed (collision footgun).
	s.Prefix = "  "
	c.McpServers["ok"] = s
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prefix")
}

func TestValidate_RejectsDuplicatePrefix(t *testing.T) {
	c := &Config{
		Version: 1,
		Daemon:  DefaultDaemon(),
		McpServers: map[string]Server{
			"a": {Command: "x", Enabled: true, Prefix: "dup"},
			"b": {Command: "x", Enabled: true, Prefix: "dup"},
		},
	}
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate prefix")
}

func TestValidate_RejectsBadServerName(t *testing.T) {
	c := validCfg()
	c.McpServers["has space"] = Server{Command: "echo", Enabled: true}
	err := Validate(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestValidate_RejectsBadLogLevel(t *testing.T) {
	c := validCfg()
	c.Daemon.LogLevel = "chatty"
	require.Error(t, Validate(c))
}
```

- [ ] **Step 2:** Run tests — compile fails (Validate undefined):

```bash
go test ./internal/config/ -run TestValidate -v
```

### Task 1.5: Validator — implement

**Files:**
- Create: `internal/config/validate.go`

- [ ] **Step 1:** Create `internal/config/validate.go`:

```go
package config

import (
	"fmt"
	"regexp"
	"strings"
)

// validServerName matches "[A-Za-z0-9_-]{1,64}".
var validServerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var allowedLogLevels = map[string]struct{}{
	"debug": {}, "info": {}, "warn": {}, "error": {},
}

// Validate checks invariants on a parsed Config. It applies no defaults —
// call after Parse (which has already defaulted Daemon fields).
func Validate(c *Config) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if c.Version != Version {
		return fmt.Errorf("unsupported config version %d (expected %d)", c.Version, Version)
	}
	if c.Daemon.HTTPPort < 1 || c.Daemon.HTTPPort > 65535 {
		return fmt.Errorf("daemon.http_port %d out of range", c.Daemon.HTTPPort)
	}
	if _, ok := allowedLogLevels[c.Daemon.LogLevel]; !ok {
		return fmt.Errorf("daemon.log_level %q must be one of debug|info|warn|error", c.Daemon.LogLevel)
	}

	seenPrefix := map[string]string{}
	for name, s := range c.McpServers {
		if !validServerName.MatchString(name) {
			return fmt.Errorf("server name %q invalid: must match [A-Za-z0-9_-]{1,64}", name)
		}
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("server %q: command must be set", name)
		}
		// Explicit empty/whitespace prefix is forbidden (collision footgun).
		// If not set at all, EffectivePrefix will use the server name.
		if s.Prefix != "" && strings.TrimSpace(s.Prefix) == "" {
			return fmt.Errorf("server %q: prefix must not be blank/whitespace when set", name)
		}
		p := EffectivePrefix(name, s)
		if !validServerName.MatchString(p) {
			return fmt.Errorf("server %q: effective prefix %q invalid", name, p)
		}
		if prev, ok := seenPrefix[p]; ok {
			return fmt.Errorf("server %q: duplicate prefix %q already used by %q", name, p, prev)
		}
		seenPrefix[p] = name
	}
	return nil
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/config/ -run TestValidate -v
```

Expected: all cases pass.

- [ ] **Step 3:** Full config package test:

```bash
go test ./internal/config/ -v
```

Expected: all tests pass.

- [ ] **Step 4:** Commit:

```bash
git add internal/config/validate.go internal/config/validate_test.go
git commit -m "feat(config): add validator (version/port/prefix/name rules)"
```

### Task 1.6: JSON schema file

**Files:**
- Create: `schema/config.schema.json`

- [ ] **Step 1:** Create `schema/config.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/ayushraj/mcp-gateway/main/schema/config.schema.json",
  "title": "mcp-gateway config",
  "type": "object",
  "required": ["version", "mcpServers"],
  "additionalProperties": false,
  "properties": {
    "$schema": { "type": "string" },
    "version": { "type": "integer", "const": 1 },
    "daemon": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "http_port": { "type": "integer", "minimum": 1, "maximum": 65535 },
        "log_level": { "type": "string", "enum": ["debug", "info", "warn", "error"] },
        "event_buffer_size": { "type": "integer", "minimum": 100 },
        "child_restart_backoff_max_seconds": { "type": "integer", "minimum": 1 },
        "child_restart_max_attempts": { "type": "integer", "minimum": 0 }
      }
    },
    "mcpServers": {
      "type": "object",
      "patternProperties": {
        "^[A-Za-z0-9_-]{1,64}$": {
          "type": "object",
          "required": ["command"],
          "additionalProperties": false,
          "properties": {
            "command": { "type": "string", "minLength": 1 },
            "args": { "type": "array", "items": { "type": "string" } },
            "env":  { "type": "object", "additionalProperties": { "type": "string" } },
            "enabled": { "type": "boolean" },
            "prefix": { "type": "string", "pattern": "^[A-Za-z0-9_-]{1,64}$" }
          }
        }
      },
      "additionalProperties": false
    }
  }
}
```

- [ ] **Step 2:** Commit:

```bash
git add schema/config.schema.json
git commit -m "docs(schema): add JSON Schema for config.jsonc"
```

### Task 1.7: Config watcher — write the test first

**Files:**
- Create: `internal/config/watcher_test.go`

- [ ] **Step 1:** Create `internal/config/watcher_test.go`:

```go
package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validJSONC = `{
	"version": 1,
	"daemon": { "http_port": 7823, "log_level": "info" },
	"mcpServers": { "%s": { "command": "echo", "enabled": true } }
}`

func writeCfg(t *testing.T, dir string, body string) string {
	t.Helper()
	p := filepath.Join(dir, "config.jsonc")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestWatcher_EmitsInitialLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, fmt.Sprintf(validJSONC, "a"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Close()

	select {
	case cfg := <-w.Changes():
		require.Contains(t, cfg.McpServers, "a")
	case <-ctx.Done():
		t.Fatal("no initial config received")
	}
}

func TestWatcher_EmitsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, fmt.Sprintf(validJSONC, "a"))

	w, err := NewWatcher(path)
	require.NoError(t, err)
	defer w.Close()

	// Drain initial.
	<-w.Changes()

	// Atomic rewrite: write-temp + rename. Mirrors how CLI mutations will write.
	tmp := path + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(fmt.Sprintf(validJSONC, "b")), 0o600))
	require.NoError(t, os.Rename(tmp, path))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case cfg := <-w.Changes():
		assert.Contains(t, cfg.McpServers, "b")
	case <-ctx.Done():
		t.Fatal("no change emitted after rename")
	}
}
```

> Tests use `context.Background()` + explicit `WithTimeout` so the plan works on Go 1.23+ without depending on Go 1.24's `testing.T.Context`.

Add the missing import:

```go
import "fmt"
```

(Append to the existing import block.)

- [ ] **Step 2:** Run — compile fails (NewWatcher undefined):

```bash
go test ./internal/config/ -run TestWatcher -v
```

### Task 1.8: Config watcher — implement

**Files:**
- Create: `internal/config/watcher.go`

- [ ] **Step 1:** Create `internal/config/watcher.go`:

```go
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher observes a config file and emits freshly-parsed Configs on change.
type Watcher struct {
	path     string
	fsw      *fsnotify.Watcher
	changes  chan *Config
	errors   chan error
	done     chan struct{}
	debounce time.Duration

	mu     sync.Mutex
	closed bool
}

// NewWatcher starts watching path and emits the initial config plus any change.
// Atomic replacements (tmp + rename), which our own CLI mutations use, are handled.
func NewWatcher(path string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	// Watch the parent directory — renames emit CREATE on the new file in that dir.
	dir := filepath.Dir(path)
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}
	w := &Watcher{
		path:     path,
		fsw:      fsw,
		changes:  make(chan *Config, 4),
		errors:   make(chan error, 4),
		done:     make(chan struct{}),
		debounce: 150 * time.Millisecond,
	}
	// Emit initial load synchronously so the caller sees it.
	if cfg, err := ParseFile(path); err == nil {
		if err := Validate(cfg); err == nil {
			w.changes <- cfg
		} else {
			w.errors <- err
		}
	} else {
		w.errors <- err
	}
	go w.loop()
	return w, nil
}

// Changes emits freshly parsed+validated Config values.
func (w *Watcher) Changes() <-chan *Config { return w.changes }

// Errors emits parse/validation errors. Non-blocking — drops if the caller isn't draining.
func (w *Watcher) Errors() <-chan error { return w.errors }

// Close stops the watcher.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	close(w.done)
	return w.fsw.Close()
}

func (w *Watcher) loop() {
	var timer *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) != filepath.Clean(w.path) {
				continue
			}
			// Act on any write/create/rename for our file.
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			// Debounce: coalesce bursts of fs events.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(w.debounce, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
		case <-fire:
			cfg, err := ParseFile(w.path)
			if err != nil {
				w.sendErr(err)
				continue
			}
			if err := Validate(cfg); err != nil {
				w.sendErr(err)
				continue
			}
			select {
			case w.changes <- cfg:
			case <-w.done:
				return
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.sendErr(err)
		}
	}
}

func (w *Watcher) sendErr(err error) {
	select {
	case w.errors <- err:
	default:
	}
	_ = errors.Unwrap(err) // keep linter happy; we may do more later
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/config/ -v
```

Expected: all tests pass.

- [ ] **Step 3:** Commit:

```bash
git add internal/config/watcher.go internal/config/watcher_test.go
git commit -m "feat(config): fsnotify-based watcher with debounce"
```

---

## Phase 2 — Supervisor

### Task 2.1: Server state + state machine — write the test first

**Files:**
- Create: `internal/supervisor/server_test.go`

- [ ] **Step 1:** Create `internal/supervisor/server_test.go`:

```go
package supervisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestState_StringAndValidTransitions(t *testing.T) {
	assert.Equal(t, "starting", StateStarting.String())
	assert.Equal(t, "running", StateRunning.String())
	assert.Equal(t, "errored", StateErrored.String())
	assert.Equal(t, "restarting", StateRestarting.String())
	assert.Equal(t, "disabled", StateDisabled.String())
	assert.Equal(t, "stopped", StateStopped.String())
}

func TestBackoff_Schedule(t *testing.T) {
	b := NewBackoff(60)
	// Sequence: 1s, 2s, 4s, 8s, 16s, 32s, 60s (capped), 60s, ...
	expectSecs := []int{1, 2, 4, 8, 16, 32, 60, 60}
	for i, want := range expectSecs {
		got := b.Next().Seconds()
		assert.InDelta(t, want, got, 0.001, "attempt %d", i)
	}
}

func TestBackoff_ResetsOnSuccess(t *testing.T) {
	b := NewBackoff(60)
	_ = b.Next() // 1s
	_ = b.Next() // 2s
	b.Reset()
	assert.InDelta(t, 1.0, b.Next().Seconds(), 0.001)
}
```

- [ ] **Step 2:** Run — fails, types undefined.

```bash
go test ./internal/supervisor/ -run "TestState|TestBackoff" -v
```

### Task 2.2: Server state + state machine — implement

**Files:**
- Create: `internal/supervisor/server.go`

- [ ] **Step 1:** Create `internal/supervisor/server.go`:

```go
package supervisor

import (
	"time"
)

// State is the lifecycle stage of a supervised child process.
type State int

const (
	StateStarting State = iota
	StateRunning
	StateErrored
	StateRestarting
	StateDisabled
	StateStopped
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateErrored:
		return "errored"
	case StateRestarting:
		return "restarting"
	case StateDisabled:
		return "disabled"
	case StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Backoff is exponential with a hard cap (1, 2, 4, 8, 16, ... maxSeconds).
type Backoff struct {
	max     time.Duration
	attempt int
}

// NewBackoff creates a Backoff capped at maxSeconds seconds.
func NewBackoff(maxSeconds int) *Backoff {
	return &Backoff{max: time.Duration(maxSeconds) * time.Second}
}

// Next returns the next delay and advances.
func (b *Backoff) Next() time.Duration {
	b.attempt++
	// 2^(attempt-1) seconds; first call → 1s.
	shift := b.attempt - 1
	if shift > 30 {
		shift = 30
	}
	d := time.Duration(1<<shift) * time.Second
	if d > b.max || d < 0 {
		d = b.max
	}
	return d
}

// Reset clears the attempt counter (call after a clean run of sufficient duration).
func (b *Backoff) Reset() { b.attempt = 0 }
```

- [ ] **Step 2:** Run:

```bash
go test ./internal/supervisor/ -run "TestState|TestBackoff" -v
```

Expected: PASS.

- [ ] **Step 3:** Commit:

```bash
git add internal/supervisor/server.go internal/supervisor/server_test.go
git commit -m "feat(supervisor): state type and exponential backoff"
```

### Task 2.3: Process spawn — write the test first

**Files:**
- Create: `internal/supervisor/process_test.go`

- [ ] **Step 1:** Create `internal/supervisor/process_test.go`:

```go
package supervisor

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpawn_LifecyclesEcho(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "echo.log")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, err := Spawn(ctx, SpawnConfig{
		Name:       "echo",
		Command:    "sh",
		Args:       []string{"-c", "echo hello; cat"}, // stay alive until stdin closed
		Env:        map[string]string{"FOO": "bar"},
		StderrPath: logPath,
	})
	require.NoError(t, err)

	// Write to stdin and read the echo back on stdout.
	_, err = io.WriteString(p.Stdin, "world\n")
	require.NoError(t, err)

	r := bufio.NewReader(p.Stdout)
	line1, _ := r.ReadString('\n')
	assert.Contains(t, line1, "hello")
	line2, _ := r.ReadString('\n')
	assert.Contains(t, line2, "world")

	// Kill and verify exit.
	require.NoError(t, p.Kill())
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit after Kill")
	}
}

func TestSpawn_CapturesStderrToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "err.log")

	p, err := Spawn(context.Background(), SpawnConfig{
		Name:       "stderrtest",
		Command:    "sh",
		Args:       []string{"-c", "echo boom >&2; sleep 0.1"},
		StderrPath: logPath,
	})
	require.NoError(t, err)
	<-p.Done()

	buf, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(buf), "boom"), "stderr: %q", string(buf))
}

func TestSpawn_MissingCommandErrors(t *testing.T) {
	_, err := Spawn(context.Background(), SpawnConfig{
		Name:    "nope",
		Command: "definitely-not-a-real-command-for-test",
	})
	require.Error(t, err)
}
```

- [ ] **Step 2:** Run — fails, `Spawn` undefined.

### Task 2.4: Process spawn — implement

**Files:**
- Create: `internal/supervisor/process.go`

- [ ] **Step 1:** Create `internal/supervisor/process.go`:

```go
package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// SpawnConfig describes how to launch a child process.
type SpawnConfig struct {
	Name       string
	Command    string
	Args       []string
	Env        map[string]string
	StderrPath string // if set, child stderr is tee'd to this file
}

// Process is a running child; wraps exec.Cmd and the three stdio pipes.
type Process struct {
	Name   string
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	doneCh chan struct{}
	err    error
}

// Spawn launches a new child and returns a Process handle.
// The child runs in its own process group so we can cleanly signal the whole tree.
func Spawn(ctx context.Context, sc SpawnConfig) (*Process, error) {
	if sc.Command == "" {
		return nil, fmt.Errorf("spawn %s: empty command", sc.Name)
	}
	cmd := exec.CommandContext(ctx, sc.Command, sc.Args...)

	// Env: inherit the parent env (users sometimes rely on PATH), then overlay ours.
	env := os.Environ()
	for k, v := range sc.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Own process group; allows Kill to signal the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("spawn %s: stdin: %w", sc.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("spawn %s: stdout: %w", sc.Name, err)
	}

	// stderr → file if requested; otherwise discard (we don't want to flood daemon output).
	var stderrWriter io.Writer = io.Discard
	var stderrFile *os.File
	if sc.StderrPath != "" {
		f, err := os.OpenFile(sc.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("spawn %s: open stderr log: %w", sc.Name, err)
		}
		stderrFile = f
		stderrWriter = f
	}
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		if stderrFile != nil {
			stderrFile.Close()
		}
		return nil, fmt.Errorf("spawn %s: start: %w", sc.Name, err)
	}

	p := &Process{
		Name:   sc.Name,
		Cmd:    cmd,
		Stdin:  stdin,
		Stdout: stdout,
		doneCh: make(chan struct{}),
	}

	go func() {
		p.err = cmd.Wait()
		if stderrFile != nil {
			stderrFile.Close()
		}
		close(p.doneCh)
	}()

	return p, nil
}

// Done is closed when the child exits.
func (p *Process) Done() <-chan struct{} { return p.doneCh }

// Err returns the process's exit error (nil if clean).
func (p *Process) Err() error {
	select {
	case <-p.doneCh:
		return p.err
	default:
		return nil
	}
}

// Kill terminates the process group with SIGTERM then SIGKILL.
func (p *Process) Kill() error {
	if p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.Cmd.Process.Pid)
	if err != nil {
		// fall back to PID
		pgid = p.Cmd.Process.Pid
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	return nil // SIGKILL fallback done by context cancel or supervisor timeout
}
```

- [ ] **Step 2:** Run:

```bash
go test ./internal/supervisor/ -v
```

Expected: all tests pass. On CI Linux runners, the echo test should work identically.

- [ ] **Step 3:** Commit:

```bash
git add internal/supervisor/process.go internal/supervisor/process_test.go
git commit -m "feat(supervisor): spawn child processes with process-group isolation"
```

### Task 2.5: Supervisor orchestrator — write the test first

**Files:**
- Create: `internal/supervisor/supervisor_test.go`

- [ ] **Step 1:** Create `internal/supervisor/supervisor_test.go`:

```go
package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustStart(t *testing.T) (*Supervisor, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(SupervisorOpts{LogDir: t.TempDir(), MaxRestartAttempts: 3, BackoffMaxSeconds: 1})
	go s.Run(ctx)
	return s, cancel
}

func waitState(t *testing.T, s *Supervisor, name string, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if snap := s.Status(name); snap.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := s.Status(name)
	t.Fatalf("server %q: wanted state %s, got %s (last err: %v)", name, want, snap.State, snap.LastError)
}

func TestSupervisor_StartsAndTracksServer(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	s.Set("echo", ServerSpec{
		Name:    "echo",
		Command: "sh",
		Args:    []string{"-c", "cat"},
	})
	waitState(t, s, "echo", StateRunning, 2*time.Second)
	require.NotNil(t, s.Process("echo"))
}

func TestSupervisor_RestartsOnCrash(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	// Process that exits immediately so the supervisor observes a crash.
	s.Set("crasher", ServerSpec{
		Name:    "crasher",
		Command: "sh",
		Args:    []string{"-c", "exit 1"},
	})
	// Should cycle through attempts and eventually land in Disabled (maxAttempts=3).
	waitState(t, s, "crasher", StateDisabled, 4*time.Second)
	snap := s.Status("crasher")
	assert.Equal(t, 3, snap.RestartCount)
}

func TestSupervisor_RemoveKillsServer(t *testing.T) {
	s, cancel := mustStart(t)
	defer cancel()
	s.Set("ok", ServerSpec{
		Name:    "ok",
		Command: "sh",
		Args:    []string{"-c", "cat"},
	})
	waitState(t, s, "ok", StateRunning, 2*time.Second)
	s.Remove("ok")
	waitState(t, s, "ok", StateStopped, 2*time.Second)
}
```

- [ ] **Step 2:** Run — fails, types undefined.

### Task 2.6: Supervisor orchestrator — implement

**Files:**
- Create: `internal/supervisor/supervisor.go`

- [ ] **Step 1:** Create `internal/supervisor/supervisor.go`:

```go
package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"
)

// ServerSpec is the supervisor's input — derived from config.Server at each reconcile.
type ServerSpec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Status is an immutable snapshot of a server's runtime state.
type Status struct {
	Name         string
	State        State
	RestartCount int
	StartedAt    time.Time
	LastError    error
}

// Supervisor orchestrates a set of child processes identified by name.
// Call Set/Remove to declare desired state; Run enforces it.
type Supervisor struct {
	opts SupervisorOpts

	mu      sync.Mutex
	servers map[string]*server
	notify  chan struct{} // wakes the run loop
	done    chan struct{}
}

// SupervisorOpts tunes restart behavior and where stderr logs land.
type SupervisorOpts struct {
	LogDir             string
	MaxRestartAttempts int
	BackoffMaxSeconds  int
}

// server is the internal per-name state; goroutine-owned (one "manager" goroutine per name).
type server struct {
	spec    ServerSpec
	desired bool // true = should be running; false = user removed it

	state    State
	restarts int
	started  time.Time
	lastErr  error

	proc   *Process
	cancel context.CancelFunc

	backoff *Backoff
	done    chan struct{}
}

// New creates a Supervisor. It is not started until you call Run.
func New(opts SupervisorOpts) *Supervisor {
	if opts.MaxRestartAttempts == 0 {
		opts.MaxRestartAttempts = 5
	}
	if opts.BackoffMaxSeconds == 0 {
		opts.BackoffMaxSeconds = 60
	}
	return &Supervisor{
		opts:    opts,
		servers: map[string]*server{},
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// Run blocks until ctx is cancelled. Reconciles on wakeup; idle otherwise.
func (s *Supervisor) Run(ctx context.Context) {
	defer close(s.done)
	for {
		s.reconcile(ctx)
		select {
		case <-ctx.Done():
			s.shutdownAll()
			return
		case <-s.notify:
		}
	}
}

// Set declares the desired state for a server. Creates or updates in place.
func (s *Supervisor) Set(name string, spec ServerSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		srv = &server{
			state:   StateStopped,
			backoff: NewBackoff(s.opts.BackoffMaxSeconds),
		}
		s.servers[name] = srv
	}
	specChanged := !specEqual(srv.spec, spec)
	srv.spec = spec
	srv.desired = true
	if specChanged && (srv.state == StateRunning || srv.state == StateStarting) {
		srv.state = StateRestarting
	}
	s.wake()
}

// Remove marks a server for teardown; it transitions to Stopped when its child exits.
func (s *Supervisor) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return
	}
	srv.desired = false
	s.wake()
}

// Status returns a snapshot for name (zero-valued if unknown).
func (s *Supervisor) Status(name string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return Status{Name: name}
	}
	return Status{
		Name:         name,
		State:        srv.state,
		RestartCount: srv.restarts,
		StartedAt:    srv.started,
		LastError:    srv.lastErr,
	}
}

// Process returns the live Process for name, or nil.
func (s *Supervisor) Process(name string) *Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.servers[name]; ok && srv.proc != nil {
		return srv.proc
	}
	return nil
}

// List returns current snapshot for all servers.
func (s *Supervisor) List() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.servers))
	for name, srv := range s.servers {
		out = append(out, Status{
			Name:         name,
			State:        srv.state,
			RestartCount: srv.restarts,
			StartedAt:    srv.started,
			LastError:    srv.lastErr,
		})
	}
	return out
}

func (s *Supervisor) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Supervisor) reconcile(ctx context.Context) {
	s.mu.Lock()
	var toStart []string
	var toKill []string
	for name, srv := range s.servers {
		switch {
		case !srv.desired && srv.proc == nil && srv.state != StateStopped:
			srv.state = StateStopped
		case !srv.desired && srv.proc != nil:
			toKill = append(toKill, name)
		case srv.desired && srv.state == StateDisabled:
			// stay disabled; user must explicitly re-set
		case srv.desired && srv.proc == nil && srv.state != StateStopped && srv.state != StateStarting:
			toStart = append(toStart, name)
		case srv.desired && srv.proc == nil && srv.state == StateStopped:
			toStart = append(toStart, name)
		case srv.desired && srv.state == StateRestarting && srv.proc != nil:
			toKill = append(toKill, name)
		}
	}
	s.mu.Unlock()

	for _, n := range toKill {
		s.killServer(n)
	}
	for _, n := range toStart {
		s.startServer(ctx, n)
	}
}

func (s *Supervisor) startServer(parentCtx context.Context, name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok || !srv.desired {
		s.mu.Unlock()
		return
	}
	srv.state = StateStarting
	spec := srv.spec
	logDir := s.opts.LogDir
	s.mu.Unlock()

	childCtx, cancel := context.WithCancel(parentCtx)
	p, err := Spawn(childCtx, SpawnConfig{
		Name:       spec.Name,
		Command:    spec.Command,
		Args:       spec.Args,
		Env:        spec.Env,
		StderrPath: filepath.Join(logDir, name+".log"),
	})
	if err != nil {
		cancel()
		s.mu.Lock()
		srv.lastErr = err
		srv.state = StateErrored
		srv.restarts++
		if srv.restarts >= s.opts.MaxRestartAttempts {
			srv.state = StateDisabled
			s.mu.Unlock()
			return
		}
		// schedule backoff retry via wake after sleep
		d := srv.backoff.Next()
		s.mu.Unlock()
		go func() {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				s.wake()
			case <-parentCtx.Done():
			}
		}()
		return
	}

	s.mu.Lock()
	srv.proc = p
	srv.cancel = cancel
	srv.started = time.Now()
	srv.state = StateRunning
	s.mu.Unlock()

	// Watch for exit.
	go func() {
		<-p.Done()
		waitErr := p.Err()
		s.mu.Lock()
		srv.proc = nil
		if srv.cancel != nil {
			srv.cancel()
			srv.cancel = nil
		}
		if !srv.desired {
			srv.state = StateStopped
			s.mu.Unlock()
			s.wake()
			return
		}
		// Unexpected exit.
		srv.lastErr = waitErr
		srv.restarts++
		if srv.restarts >= s.opts.MaxRestartAttempts {
			srv.state = StateDisabled
			s.mu.Unlock()
			return
		}
		srv.state = StateErrored
		d := srv.backoff.Next()
		s.mu.Unlock()
		go func() {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				s.wake()
			case <-parentCtx.Done():
			}
		}()
	}()
}

func (s *Supervisor) killServer(name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok || srv.proc == nil {
		s.mu.Unlock()
		return
	}
	p := srv.proc
	s.mu.Unlock()
	_ = p.Kill()
	// The goroutine registered in startServer handles post-exit state.
}

func (s *Supervisor) shutdownAll() {
	s.mu.Lock()
	var procs []*Process
	for _, srv := range s.servers {
		if srv.proc != nil {
			procs = append(procs, srv.proc)
		}
	}
	s.mu.Unlock()
	for _, p := range procs {
		_ = p.Kill()
	}
	// Give children a moment to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		any := false
		for _, srv := range s.servers {
			if srv.proc != nil {
				any = true
				break
			}
		}
		s.mu.Unlock()
		if !any {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = errors.New("shutdown timed out") // placeholder for logger injection later
}

func specEqual(a, b ServerSpec) bool {
	if a.Name != b.Name || a.Command != b.Command {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for k, v := range a.Env {
		if b.Env[k] != v {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/supervisor/ -race -v
```

Expected: all tests pass. (If `TestSupervisor_RestartsOnCrash` is flaky under high load, raise the `4*time.Second` timeout.)

- [ ] **Step 3:** Commit:

```bash
git add internal/supervisor/supervisor.go internal/supervisor/supervisor_test.go
git commit -m "feat(supervisor): orchestrate N servers with restart + disable"
```

---

## Phase 3 — MCP downstream client

> **Context note to the implementing engineer:** The official Go SDK is `github.com/modelcontextprotocol/go-sdk`. At the time this plan was written, the client API surface includes (subject to change): `mcp.NewClient(...)`, `client.Connect(transport)`, `client.ListTools(ctx)`, `client.CallTool(ctx, req)`, `client.ListResources(ctx)`, `client.ReadResource(ctx, uri)`, etc. If the SDK's exported names differ from what's shown below, **prefer the SDK's names over what this plan says** and update the plan file at the end. The interface goals are what matter: given a supervised child's stdin/stdout pipes, connect an MCP client; list tools/resources/prompts; call them; receive list-changed notifications.

### Task 3.1: MCP child client — write the test first

**Files:**
- Create: `internal/testutil/fakechild/fakechild.go`
- Create: `internal/mcpchild/client_test.go`

- [ ] **Step 1:** Create a **fake stdio MCP server** we use as a test child. Create `internal/testutil/fakechild/fakechild.go`:

```go
// Package fakechild is a tiny in-memory stdio MCP server used in tests.
// It implements the minimum protocol surface for initialize/tools.list/tools.call.
// Kept hand-rolled (no SDK dependency in the test child binary) so we're testing
// our adapter against plain JSON-RPC frames, not the SDK against itself.
package fakechild

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Tool is a minimal schema for tools this fake advertises.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Server is a stdio JSON-RPC server.
type Server struct {
	mu    sync.Mutex
	tools []Tool
	// onCall is called when tools/call fires; should return (content, isError).
	onCall func(name string, args json.RawMessage) ([]any, bool)
}

// New creates a Server with the given tools and tool-call handler.
func New(tools []Tool, onCall func(string, json.RawMessage) ([]any, bool)) *Server {
	return &Server{tools: tools, onCall: onCall}
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads newline-delimited JSON-RPC frames from in and writes responses to out.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	bw := bufio.NewWriter(out)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var req rpcReq
			if e := json.Unmarshal(line, &req); e != nil {
				continue
			}
			resp := s.handle(req)
			if resp != nil {
				b, _ := json.Marshal(resp)
				bw.Write(b)
				bw.WriteByte('\n')
				bw.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handle(req rpcReq) *rpcResp {
	switch req.Method {
	case "initialize":
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{"name": "fakechild", "version": "0.0.1"},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil // no response to notifications
	case "tools/list":
		s.mu.Lock()
		out := make([]Tool, len(s.tools))
		copy(out, s.tools)
		s.mu.Unlock()
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": out}}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		content, isErr := []any{}, false
		if s.onCall != nil {
			content, isErr = s.onCall(params.Name, params.Arguments)
		}
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": content,
			"isError": isErr,
		}}
	default:
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Error: &rpcErr{
			Code:    -32601,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		}}
	}
}

// StringContent returns a text content block.
func StringContent(s string) map[string]any {
	return map[string]any{"type": "text", "text": s}
}

// MustRaw marshals v or panics — handy in tests.
func MustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// LineCount — stub to keep imports clean if strings unused.
var _ = strings.TrimSpace
```

- [ ] **Step 2:** Create `internal/mcpchild/client_test.go`:

```go
package mcpchild

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayushraj/mcp-gateway/internal/testutil/fakechild"
)

// newPipedChild creates a goroutine-hosted fake stdio MCP server and returns
// in-memory stdin/stdout pipes connecting to it, plus a cleanup function.
func newPipedChild(t *testing.T, tools []fakechild.Tool,
	onCall func(string, json.RawMessage) ([]any, bool),
) (stdin io.WriteCloser, stdout io.ReadCloser, cleanup func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := fakechild.New(tools, onCall)
	done := make(chan struct{})
	go func() {
		_ = s.Serve(inR, outW)
		outW.Close()
		close(done)
	}()
	cleanup = func() {
		inW.Close()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
	return inW, outR, cleanup
}

func TestClient_InitializeAndListTools(t *testing.T) {
	tools := []fakechild.Tool{
		{Name: "hello", Description: "say hi", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
		{Name: "world", Description: "say world", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	}
	in, out, cleanup := newPipedChild(t, tools, nil)
	defer cleanup()

	c := New("fake", in, out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	got, err := c.ListTools(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	names := []string{got[0].Name, got[1].Name}
	assert.ElementsMatch(t, []string{"hello", "world"}, names)
}

func TestClient_CallTool(t *testing.T) {
	tools := []fakechild.Tool{{Name: "echo", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})}}
	onCall := func(name string, args json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("ok:" + string(args))}, false
	}
	in, out, cleanup := newPipedChild(t, tools, onCall)
	defer cleanup()

	c := New("fake", in, out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))

	res, err := c.CallTool(ctx, "echo", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, _ := res.Content[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, "ok:")
}
```

- [ ] **Step 3:** Run — compile fails (`mcpchild.New`, `.Initialize`, `.ListTools`, `.CallTool` undefined):

```bash
go test ./internal/mcpchild/ -v
```

### Task 3.2: MCP child client — implement

**Files:**
- Create: `internal/mcpchild/client.go`

- [ ] **Step 1:** Create `internal/mcpchild/client.go`. **Implementation note:** this uses **hand-rolled JSON-RPC over stdio** — not the SDK — for the downstream side, because the SDK at time of writing doesn't cleanly accept arbitrary `io.ReadCloser`/`io.WriteCloser` pipes as a transport in all versions. If by the time you're reading this the SDK exposes a `stdio.NewTransport(stdin, stdout)` or similar, delete this file and use the SDK. The interface below is what the aggregator depends on; keep it stable.

```go
// Package mcpchild implements an MCP client that speaks JSON-RPC over stdio
// pipes (intended for a supervised child process).
package mcpchild

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Tool is what list_tools returns (kept minimal to avoid SDK coupling).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Resource mirrors the MCP resources/list shape we need.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// Prompt mirrors prompts/list.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument mirrors the prompts/list argument descriptor.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// CallResult is returned from tools/call.
type CallResult struct {
	Content []any `json:"content"`
	IsError bool  `json:"isError"`
}

// Client is an MCP client for one downstream child.
type Client struct {
	Name string

	in  io.WriteCloser
	out io.ReadCloser
	br  *bufio.Reader

	nextID atomic.Int64
	mu     sync.Mutex
	inflight map[string]chan *rpcResp

	// notify callbacks (set via Subscribe* methods):
	onToolsListChanged     func()
	onResourcesListChanged func()
	onPromptsListChanged   func()
	onResourceUpdated      func(uri string)
}

// New creates a Client bound to a child's stdio.
func New(name string, in io.WriteCloser, out io.ReadCloser) *Client {
	return &Client{
		Name:     name,
		in:       in,
		out:      out,
		br:       bufio.NewReader(out),
		inflight: map[string]chan *rpcResp{},
	}
}

// Initialize performs the MCP initialize handshake and starts the frame reader.
func (c *Client) Initialize(ctx context.Context) error {
	go c.readLoop()
	_, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mcp-gateway", "version": "0.1"},
	})
	if err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

// ListTools calls tools/list.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.request(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Tools, nil
}

// ListResources calls resources/list.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	raw, err := c.request(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Resources []Resource `json:"resources"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Resources, nil
}

// ListPrompts calls prompts/list.
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	raw, err := c.request(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Prompts []Prompt `json:"prompts"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Prompts, nil
}

// CallTool invokes tools/call.
func (c *Client) CallTool(ctx context.Context, name string, args any) (*CallResult, error) {
	raw, err := c.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ReadResource calls resources/read.
func (c *Client) ReadResource(ctx context.Context, uri string) (json.RawMessage, error) {
	return c.request(ctx, "resources/read", map[string]any{"uri": uri})
}

// GetPrompt calls prompts/get.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (json.RawMessage, error) {
	return c.request(ctx, "prompts/get", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

// OnToolsListChanged registers a callback for tools/list_changed notifications.
func (c *Client) OnToolsListChanged(cb func())     { c.onToolsListChanged = cb }
// OnResourcesListChanged registers a callback for resources/list_changed.
func (c *Client) OnResourcesListChanged(cb func()) { c.onResourcesListChanged = cb }
// OnPromptsListChanged registers a callback for prompts/list_changed.
func (c *Client) OnPromptsListChanged(cb func())   { c.onPromptsListChanged = cb }
// OnResourceUpdated registers a callback for resources/updated(uri).
func (c *Client) OnResourceUpdated(cb func(uri string)) { c.onResourceUpdated = cb }

// Close shuts the client (pipes are owned by the supervisor).
func (c *Client) Close() error { return nil }

// ---- internal wire protocol ----

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"` // notifications have no id
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := fmt.Sprintf("%d", c.nextID.Add(1))
	ch := make(chan *rpcResp, 1)
	c.mu.Lock()
	c.inflight[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
	}()

	buf, err := json.Marshal(rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if _, err := c.in.Write(append(buf, '\n')); err != nil {
		return nil, err
	}

	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s (code %d)", method, r.Error.Message, r.Error.Code)
		}
		return r.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	buf, err := json.Marshal(rpcReq{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(buf, '\n'))
	return err
}

func (c *Client) readLoop() {
	for {
		line, err := c.br.ReadBytes('\n')
		if len(line) > 0 {
			var r rpcResp
			if json.Unmarshal(line, &r) == nil {
				c.dispatch(&r)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
	}
}

func (c *Client) dispatch(r *rpcResp) {
	if r.ID != "" {
		c.mu.Lock()
		ch, ok := c.inflight[r.ID]
		c.mu.Unlock()
		if ok {
			ch <- r
		}
		return
	}
	// Notification from the child.
	switch r.Method {
	case "notifications/tools/list_changed":
		if c.onToolsListChanged != nil {
			c.onToolsListChanged()
		}
	case "notifications/resources/list_changed":
		if c.onResourcesListChanged != nil {
			c.onResourcesListChanged()
		}
	case "notifications/prompts/list_changed":
		if c.onPromptsListChanged != nil {
			c.onPromptsListChanged()
		}
	case "notifications/resources/updated":
		if c.onResourceUpdated != nil {
			var p struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(r.Params, &p)
			c.onResourceUpdated(p.URI)
		}
	}
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/mcpchild/ -race -v
```

Expected: all pass.

- [ ] **Step 3:** Commit:

```bash
git add internal/mcpchild/ internal/testutil/fakechild/
git commit -m "feat(mcpchild): JSON-RPC MCP client for a supervised stdio child"
```

---

## Phase 4 — Aggregator

### Task 4.1: Prefixer — write the test first

**Files:**
- Create: `internal/aggregator/prefix_test.go`

- [ ] **Step 1:** Create `internal/aggregator/prefix_test.go`:

```go
package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrefix_RoundTrip(t *testing.T) {
	p := "github"
	prefixed := PrefixTool(p, "create_issue")
	assert.Equal(t, "github__create_issue", prefixed)
	server, original, ok := ParsePrefixed(prefixed)
	assert.True(t, ok)
	assert.Equal(t, "github", server)
	assert.Equal(t, "create_issue", original)
}

func TestPrefix_NameWithInternalDoubleUnderscore(t *testing.T) {
	// "weird__tool" as original name should still parse correctly.
	prefixed := PrefixTool("ns", "weird__tool")
	assert.Equal(t, "ns__weird__tool", prefixed)
	server, original, ok := ParsePrefixed(prefixed)
	assert.True(t, ok)
	assert.Equal(t, "ns", server)
	assert.Equal(t, "weird__tool", original)
}

func TestPrefix_RejectsUnprefixed(t *testing.T) {
	_, _, ok := ParsePrefixed("no_prefix_here")
	assert.False(t, ok)
}

func TestPrefix_Resource(t *testing.T) {
	// scheme preserved; prefix added as first path segment.
	assert.Equal(t, "github+mcp://repos/foo", PrefixResourceURI("github", "mcp://repos/foo"))
	// opaque (no ://) URIs get a bare prefix.
	assert.Equal(t, "github__note-1", PrefixResourceURI("github", "note-1"))
	server, orig, ok := ParsePrefixedResourceURI("github+mcp://repos/foo")
	assert.True(t, ok)
	assert.Equal(t, "github", server)
	assert.Equal(t, "mcp://repos/foo", orig)
	server, orig, ok = ParsePrefixedResourceURI("github__note-1")
	assert.True(t, ok)
	assert.Equal(t, "github", server)
	assert.Equal(t, "note-1", orig)
}
```

- [ ] **Step 2:** Run — fails: types undefined.

### Task 4.2: Prefixer — implement

**Files:**
- Create: `internal/aggregator/prefix.go`

- [ ] **Step 1:** Create `internal/aggregator/prefix.go`:

```go
package aggregator

import "strings"

// Sep is the double-underscore separator used for tool/prompt names.
const Sep = "__"

// PrefixTool joins a server prefix and a tool name with "__".
func PrefixTool(prefix, name string) string { return prefix + Sep + name }

// ParsePrefixed splits a prefixed name back into (prefix, original, ok).
// Uses the FIRST "__" as the boundary so internal "__" in tool names is preserved.
func ParsePrefixed(prefixed string) (string, string, bool) {
	i := strings.Index(prefixed, Sep)
	if i <= 0 || i+len(Sep) >= len(prefixed) {
		return "", "", false
	}
	return prefixed[:i], prefixed[i+len(Sep):], true
}

// PrefixResourceURI returns a prefixed URI. For schemed URIs like "mcp://..."
// we produce "<prefix>+mcp://..." so clients can still see the scheme. For
// opaque URIs (no "://") we use the same "__" scheme as tools.
func PrefixResourceURI(prefix, uri string) string {
	if idx := strings.Index(uri, "://"); idx > 0 {
		return prefix + "+" + uri
	}
	return prefix + Sep + uri
}

// ParsePrefixedResourceURI is the inverse of PrefixResourceURI.
func ParsePrefixedResourceURI(uri string) (string, string, bool) {
	// Try the "<prefix>+<scheme>://..." form first.
	if plus := strings.Index(uri, "+"); plus > 0 {
		colonSlash := strings.Index(uri, "://")
		if colonSlash > plus {
			return uri[:plus], uri[plus+1:], true
		}
	}
	// Fall back to "<prefix>__<rest>".
	return ParsePrefixed(uri)
}
```

- [ ] **Step 2:** Run:

```bash
go test ./internal/aggregator/ -run TestPrefix -v
```

Expected: PASS.

- [ ] **Step 3:** Commit:

```bash
git add internal/aggregator/prefix.go internal/aggregator/prefix_test.go
git commit -m "feat(aggregator): tool/resource prefixing helpers"
```

### Task 4.3: Aggregator — write the test first

**Files:**
- Create: `internal/aggregator/aggregator_test.go`

- [ ] **Step 1:** Create `internal/aggregator/aggregator_test.go`:

```go
package aggregator

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayushraj/mcp-gateway/internal/mcpchild"
	"github.com/ayushraj/mcp-gateway/internal/testutil/fakechild"
)

func newClient(t *testing.T, name string, tools []fakechild.Tool) (*mcpchild.Client, func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := fakechild.New(tools, func(n string, args json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("called:" + name + "/" + n)}, false
	})
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(inR, outW)
		outW.Close()
		close(done)
	}()
	c := mcpchild.New(name, inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))
	cleanup := func() {
		inW.Close()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
	return c, cleanup
}

func TestAggregator_MergesToolsFromTwoChildren(t *testing.T) {
	a := New()
	clientA, cleanA := newClient(t, "a", []fakechild.Tool{
		{Name: "foo", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanA()
	clientB, cleanB := newClient(t, "b", []fakechild.Tool{
		{Name: "bar", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanB()

	a.AddServer("alpha", clientA)
	a.AddServer("beta", clientB)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, a.RefreshAll(ctx))

	tools := a.Tools()
	require.Len(t, tools, 2)
	names := []string{tools[0].Name, tools[1].Name}
	assert.ElementsMatch(t, []string{"alpha__foo", "beta__bar"}, names)
}

func TestAggregator_RoutesToolCall(t *testing.T) {
	a := New()
	clientA, cleanA := newClient(t, "a", []fakechild.Tool{
		{Name: "say", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanA()
	a.AddServer("alpha", clientA)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, a.RefreshAll(ctx))

	res, err := a.CallTool(ctx, "alpha__say", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	content := res.Content[0].(map[string]any)
	assert.Contains(t, content["text"].(string), "called:a/say")
}

func TestAggregator_EmitsListChangedOnServerRemoved(t *testing.T) {
	a := New()
	clientA, cleanA := newClient(t, "a", []fakechild.Tool{
		{Name: "foo", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	})
	defer cleanA()
	a.AddServer("alpha", clientA)

	var ticks atomic.Int32
	a.OnToolsChanged(func() { ticks.Add(1) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, a.RefreshAll(ctx))
	assert.GreaterOrEqual(t, int(ticks.Load()), 1)

	before := ticks.Load()
	a.RemoveServer("alpha")
	assert.Greater(t, ticks.Load(), before)
	assert.Empty(t, a.Tools())
}
```

- [ ] **Step 2:** Run — fails: types undefined.

### Task 4.4: Aggregator — implement

**Files:**
- Create: `internal/aggregator/aggregator.go`

- [ ] **Step 1:** Create `internal/aggregator/aggregator.go`:

```go
package aggregator

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/ayushraj/mcp-gateway/internal/mcpchild"
)

// Aggregator merges tools/resources/prompts from N child MCP clients and
// exposes the merged views upstream. Server key is the server prefix.
type Aggregator struct {
	mu        sync.RWMutex
	servers   map[string]*mcpchild.Client // key = prefix
	tools     []Tool                      // merged, sorted by prefixed name
	resources []Resource
	prompts   []Prompt

	// subscriber callbacks (fire synchronously; keep them cheap)
	onToolsChanged     []func()
	onResourcesChanged []func()
	onPromptsChanged   []func()
}

// Tool is an aggregator-level tool (prefixed name + origin).
type Tool struct {
	Name        string          // prefixed, e.g. "github__create_issue"
	Description string
	InputSchema []byte
	Server      string
}

// Resource is aggregator-level.
type Resource struct {
	URI         string // prefixed
	Name        string
	Description string
	MimeType    string
	Server      string
}

// Prompt is aggregator-level.
type Prompt struct {
	Name        string // prefixed
	Description string
	Arguments   []PromptArgument
	Server      string
}

// PromptArgument mirrors the mcpchild shape (kept lightweight here).
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// New returns an empty Aggregator.
func New() *Aggregator {
	return &Aggregator{servers: map[string]*mcpchild.Client{}}
}

// AddServer wires a prefix → client mapping and subscribes to list_changed.
// Does not refresh lists; call RefreshAll (or Refresh) after adding.
func (a *Aggregator) AddServer(prefix string, c *mcpchild.Client) {
	a.mu.Lock()
	a.servers[prefix] = c
	a.mu.Unlock()
	c.OnToolsListChanged(func() { _ = a.RefreshTools(context.Background()) })
	c.OnResourcesListChanged(func() { _ = a.RefreshResources(context.Background()) })
	c.OnPromptsListChanged(func() { _ = a.RefreshPrompts(context.Background()) })
}

// RemoveServer drops the prefix. Calls list-changed callbacks.
func (a *Aggregator) RemoveServer(prefix string) {
	a.mu.Lock()
	delete(a.servers, prefix)
	a.mu.Unlock()
	_ = a.RefreshTools(context.Background())
	_ = a.RefreshResources(context.Background())
	_ = a.RefreshPrompts(context.Background())
}

// RefreshAll refreshes tools, resources, and prompts.
func (a *Aggregator) RefreshAll(ctx context.Context) error {
	if err := a.RefreshTools(ctx); err != nil {
		return err
	}
	if err := a.RefreshResources(ctx); err != nil {
		return err
	}
	return a.RefreshPrompts(ctx)
}

// RefreshTools rebuilds the merged tool list and emits a change callback.
func (a *Aggregator) RefreshTools(ctx context.Context) error {
	a.mu.RLock()
	servers := maps(a.servers)
	a.mu.RUnlock()

	var merged []Tool
	for prefix, c := range servers {
		tools, err := c.ListTools(ctx)
		if err != nil {
			return fmt.Errorf("aggregator: tools/list %s: %w", prefix, err)
		}
		for _, t := range tools {
			merged = append(merged, Tool{
				Name:        PrefixTool(prefix, t.Name),
				Description: t.Description,
				InputSchema: []byte(t.InputSchema),
				Server:      prefix,
			})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })

	a.mu.Lock()
	a.tools = merged
	cbs := append([]func(){}, a.onToolsChanged...)
	a.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return nil
}

// RefreshResources rebuilds the merged resource list.
func (a *Aggregator) RefreshResources(ctx context.Context) error {
	a.mu.RLock()
	servers := maps(a.servers)
	a.mu.RUnlock()

	var merged []Resource
	for prefix, c := range servers {
		rs, err := c.ListResources(ctx)
		if err != nil {
			// Some servers don't support resources — treat "method not found" as empty.
			continue
		}
		for _, r := range rs {
			merged = append(merged, Resource{
				URI:         PrefixResourceURI(prefix, r.URI),
				Name:        r.Name,
				Description: r.Description,
				MimeType:    r.MimeType,
				Server:      prefix,
			})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].URI < merged[j].URI })

	a.mu.Lock()
	a.resources = merged
	cbs := append([]func(){}, a.onResourcesChanged...)
	a.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return nil
}

// RefreshPrompts rebuilds the merged prompt list.
func (a *Aggregator) RefreshPrompts(ctx context.Context) error {
	a.mu.RLock()
	servers := maps(a.servers)
	a.mu.RUnlock()

	var merged []Prompt
	for prefix, c := range servers {
		ps, err := c.ListPrompts(ctx)
		if err != nil {
			continue
		}
		for _, p := range ps {
			args := make([]PromptArgument, len(p.Arguments))
			for i, a := range p.Arguments {
				args[i] = PromptArgument{Name: a.Name, Description: a.Description, Required: a.Required}
			}
			merged = append(merged, Prompt{
				Name:        PrefixTool(prefix, p.Name),
				Description: p.Description,
				Arguments:   args,
				Server:      prefix,
			})
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })

	a.mu.Lock()
	a.prompts = merged
	cbs := append([]func(){}, a.onPromptsChanged...)
	a.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return nil
}

// Tools returns a snapshot of the merged tool list.
func (a *Aggregator) Tools() []Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Tool(nil), a.tools...)
}

// Resources returns a snapshot of the merged resource list.
func (a *Aggregator) Resources() []Resource {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Resource(nil), a.resources...)
}

// Prompts returns a snapshot of the merged prompt list.
func (a *Aggregator) Prompts() []Prompt {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Prompt(nil), a.prompts...)
}

// CallTool routes a prefixed tool name to the correct child.
func (a *Aggregator) CallTool(ctx context.Context, prefixedName string, args any) (*mcpchild.CallResult, error) {
	prefix, original, ok := ParsePrefixed(prefixedName)
	if !ok {
		return nil, fmt.Errorf("tool %q has no valid prefix", prefixedName)
	}
	a.mu.RLock()
	c, ok := a.servers[prefix]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no server registered for prefix %q", prefix)
	}
	return c.CallTool(ctx, original, args)
}

// ReadResource routes resources/read to the correct child.
func (a *Aggregator) ReadResource(ctx context.Context, prefixedURI string) ([]byte, error) {
	prefix, original, ok := ParsePrefixedResourceURI(prefixedURI)
	if !ok {
		return nil, fmt.Errorf("resource URI %q has no valid prefix", prefixedURI)
	}
	a.mu.RLock()
	c, ok := a.servers[prefix]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no server registered for prefix %q", prefix)
	}
	raw, err := c.ReadResource(ctx, original)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// GetPrompt routes prompts/get to the correct child.
func (a *Aggregator) GetPrompt(ctx context.Context, prefixedName string, args map[string]string) ([]byte, error) {
	prefix, original, ok := ParsePrefixed(prefixedName)
	if !ok {
		return nil, fmt.Errorf("prompt %q has no valid prefix", prefixedName)
	}
	a.mu.RLock()
	c, ok := a.servers[prefix]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no server registered for prefix %q", prefix)
	}
	raw, err := c.GetPrompt(ctx, original, args)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// OnToolsChanged registers a callback fired after tools rebuild.
func (a *Aggregator) OnToolsChanged(cb func()) {
	a.mu.Lock()
	a.onToolsChanged = append(a.onToolsChanged, cb)
	a.mu.Unlock()
}
// OnResourcesChanged registers a callback fired after resources rebuild.
func (a *Aggregator) OnResourcesChanged(cb func()) {
	a.mu.Lock()
	a.onResourcesChanged = append(a.onResourcesChanged, cb)
	a.mu.Unlock()
}
// OnPromptsChanged registers a callback fired after prompts rebuild.
func (a *Aggregator) OnPromptsChanged(cb func()) {
	a.mu.Lock()
	a.onPromptsChanged = append(a.onPromptsChanged, cb)
	a.mu.Unlock()
}

func maps(m map[string]*mcpchild.Client) map[string]*mcpchild.Client {
	cp := make(map[string]*mcpchild.Client, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/aggregator/ -race -v
```

Expected: all pass.

- [ ] **Step 3:** Commit:

```bash
git add internal/aggregator/aggregator.go internal/aggregator/aggregator_test.go
git commit -m "feat(aggregator): merge + route tools/resources/prompts with prefixing"
```

---

## Phase 5 — Upstream HTTP server

### Task 5.1: Upstream MCP HTTP handler — write the test first

> **Design:** we implement a minimal **Streamable HTTP** endpoint at `POST /mcp` where each request is a JSON-RPC call from the client and the response body is either the JSON response or an SSE stream (`text/event-stream`) for methods that stream. For v0 we only implement the **POST** half (synchronous request/response). Server-initiated streams (SSE `GET /mcp`) are v1. This covers: initialize, tools/list, tools/call, resources/*, prompts/*.

**Files:**
- Create: `internal/daemon/http_test.go`

- [ ] **Step 1:** Create `internal/daemon/http_test.go`:

```go
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayushraj/mcp-gateway/internal/aggregator"
	"github.com/ayushraj/mcp-gateway/internal/mcpchild"
	"github.com/ayushraj/mcp-gateway/internal/testutil/fakechild"
)

func setupAggregator(t *testing.T) *aggregator.Aggregator {
	t.Helper()
	agg := aggregator.New()
	tools := []fakechild.Tool{{Name: "ping", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := fakechild.New(tools, func(name string, _ json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("pong")}, false
	})
	go func() { _ = srv.Serve(inR, outW); outW.Close() }()
	c := mcpchild.New("a", inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))
	agg.AddServer("alpha", c)
	require.NoError(t, agg.RefreshAll(ctx))
	return agg
}

func postJSON(t *testing.T, h http.Handler, body any) map[string]any {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestMCP_Initialize(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg)
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	result, ok := out["result"].(map[string]any)
	require.True(t, ok)
	caps, ok := result["capabilities"].(map[string]any)
	require.True(t, ok)
	_, hasTools := caps["tools"]
	assert.True(t, hasTools)
}

func TestMCP_ToolsList(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg)
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tools/list",
	})
	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)
	require.Len(t, tools, 1)
	first := tools[0].(map[string]any)
	assert.Equal(t, "alpha__ping", first["name"])
}

func TestMCP_ToolsCall(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg)
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "3", "method": "tools/call",
		"params": map[string]any{"name": "alpha__ping", "arguments": map[string]any{}},
	})
	result := out["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	assert.Equal(t, "pong", first["text"])
}

func TestMCP_RejectsUnknownMethod(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg)
	out := postJSON(t, h, map[string]any{
		"jsonrpc": "2.0", "id": "4", "method": "no/such/method",
	})
	errObj, ok := out["error"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, -32601, errObj["code"])
}

func TestMCP_RejectsNonPost(t *testing.T) {
	agg := setupAggregator(t)
	h := NewMCPHandler(agg)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
```

- [ ] **Step 2:** Run — fails, `NewMCPHandler` undefined.

### Task 5.2: Upstream MCP handler — implement

**Files:**
- Create: `internal/daemon/http.go`

- [ ] **Step 1:** Create `internal/daemon/http.go`:

```go
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ayushraj/mcp-gateway/internal/aggregator"
)

// NewMCPHandler returns an http.Handler that implements the POST /mcp half of
// the Streamable HTTP transport. All JSON-RPC requests must be POSTs; the body
// is a single JSON-RPC request; response is a single JSON-RPC response.
// Server-initiated streams (SSE on GET /mcp) are out of scope for v0.
func NewMCPHandler(agg *aggregator.Aggregator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var req rpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, nil, -32700, "parse error: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		resp := dispatch(ctx, agg, req)
		_ = json.NewEncoder(w).Encode(resp)
	})
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func dispatch(ctx context.Context, agg *aggregator.Aggregator, req rpcReq) rpcResp {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{"listChanged": true},
				"prompts":   map[string]any{"listChanged": true},
				"logging":   map[string]any{},
			},
			"serverInfo": map[string]any{"name": "mcp-gateway", "version": "0.1"},
		})
	case "notifications/initialized", "notifications/cancelled", "ping":
		return ok(req.ID, map[string]any{})
	case "tools/list":
		tools := agg.Tools()
		out := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			out = append(out, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": json.RawMessage(t.InputSchema),
			})
		}
		return ok(req.ID, map[string]any{"tools": out})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params: "+err.Error())
		}
		var args any
		if len(p.Arguments) > 0 {
			_ = json.Unmarshal(p.Arguments, &args)
		}
		res, err := agg.CallTool(ctx, p.Name, args)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		return ok(req.ID, map[string]any{"content": res.Content, "isError": res.IsError})
	case "resources/list":
		out := make([]map[string]any, 0)
		for _, r := range agg.Resources() {
			out = append(out, map[string]any{
				"uri":         r.URI,
				"name":        r.Name,
				"description": r.Description,
				"mimeType":    r.MimeType,
			})
		}
		return ok(req.ID, map[string]any{"resources": out})
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params: "+err.Error())
		}
		raw, err := agg.ReadResource(ctx, p.URI)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		var payload any
		_ = json.Unmarshal(raw, &payload)
		return ok(req.ID, payload)
	case "prompts/list":
		out := make([]map[string]any, 0)
		for _, p := range agg.Prompts() {
			args := make([]map[string]any, 0, len(p.Arguments))
			for _, a := range p.Arguments {
				args = append(args, map[string]any{
					"name":        a.Name,
					"description": a.Description,
					"required":    a.Required,
				})
			}
			out = append(out, map[string]any{
				"name":        p.Name,
				"description": p.Description,
				"arguments":   args,
			})
		}
		return ok(req.ID, map[string]any{"prompts": out})
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, "invalid params: "+err.Error())
		}
		raw, err := agg.GetPrompt(ctx, p.Name, p.Arguments)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		var payload any
		_ = json.Unmarshal(raw, &payload)
		return ok(req.ID, payload)
	default:
		return fail(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func ok(id json.RawMessage, result any) rpcResp {
	return rpcResp{JSONRPC: "2.0", ID: id, Result: result}
}
func fail(id json.RawMessage, code int, msg string) rpcResp {
	return rpcResp{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}
func writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	_ = json.NewEncoder(w).Encode(fail(id, code, msg))
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/daemon/ -race -v
```

Expected: all five handler tests pass.

- [ ] **Step 3:** Commit:

```bash
git add internal/daemon/http.go internal/daemon/http_test.go
git commit -m "feat(daemon): POST /mcp handler dispatching to aggregator"
```

---

## Phase 6 — Daemon wiring

### Task 6.1: Daemon struct and lifecycle

**Files:**
- Create: `internal/daemon/daemon.go`

- [ ] **Step 1:** Create `internal/daemon/daemon.go`:

```go
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ayushraj/mcp-gateway/internal/aggregator"
	"github.com/ayushraj/mcp-gateway/internal/config"
	"github.com/ayushraj/mcp-gateway/internal/mcpchild"
	"github.com/ayushraj/mcp-gateway/internal/supervisor"
)

// Daemon wires config → supervisor → aggregator → HTTP server.
type Daemon struct {
	Home   string // ~/.mcp-gateway by default
	Logger *slog.Logger

	mu       sync.Mutex
	cfg      *config.Config
	sup      *supervisor.Supervisor
	agg      *aggregator.Aggregator
	clients  map[string]*mcpchild.Client // key = prefix
}

// New creates a Daemon with defaults.
func New(home string, logger *slog.Logger) *Daemon {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Daemon{
		Home:    home,
		Logger:  logger,
		clients: map[string]*mcpchild.Client{},
	}
}

// Run boots the daemon until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	configPath := filepath.Join(d.Home, "config.jsonc")
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found at %s: %w", configPath, err)
	}

	watcher, err := config.NewWatcher(configPath)
	if err != nil {
		return fmt.Errorf("config watcher: %w", err)
	}
	defer watcher.Close()

	// Wait for initial config.
	var initial *config.Config
	select {
	case initial = <-watcher.Changes():
	case err := <-watcher.Errors():
		return fmt.Errorf("initial config: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	logDir := filepath.Join(d.Home, "servers")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("log dir: %w", err)
	}

	d.agg = aggregator.New()
	d.sup = supervisor.New(supervisor.SupervisorOpts{
		LogDir:             logDir,
		MaxRestartAttempts: initial.Daemon.ChildRestartMaxAttempts,
		BackoffMaxSeconds:  initial.Daemon.ChildRestartBackoffMaxSeconds,
	})
	go d.sup.Run(ctx)

	d.reconcile(ctx, initial)

	// HTTP server on 127.0.0.1:PORT.
	mux := http.NewServeMux()
	mux.Handle("/mcp", NewMCPHandler(d.agg))
	addr := fmt.Sprintf("127.0.0.1:%d", initial.Daemon.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	d.Logger.Info("mcp-gateway listening", "addr", addr)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			d.Logger.Error("http serve", "err", err)
		}
	}()

	for {
		select {
		case cfg := <-watcher.Changes():
			d.Logger.Info("config changed, reconciling")
			d.reconcile(ctx, cfg)
		case err := <-watcher.Errors():
			d.Logger.Error("config error", "err", err)
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shutCtx)
			cancel()
			return nil
		}
	}
}

// reconcile synchronizes supervisor + aggregator with the latest config.
// For v0: for each enabled server in cfg, ensure supervisor has it (Set)
// and a client is attached to the aggregator. For servers removed from
// cfg, tear down.
func (d *Daemon) reconcile(ctx context.Context, cfg *config.Config) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg

	wanted := map[string]struct{}{}
	for name, s := range cfg.McpServers {
		if !s.Enabled {
			continue
		}
		prefix := config.EffectivePrefix(name, s)
		wanted[prefix] = struct{}{}
		// Update supervisor desired state.
		d.sup.Set(name, supervisor.ServerSpec{
			Name:    name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
		})
		// Attach client if not already.
		if _, ok := d.clients[prefix]; !ok {
			if p := d.sup.Process(name); p != nil {
				client := mcpchild.New(name, p.Stdin, p.Stdout)
				if err := client.Initialize(ctx); err != nil {
					d.Logger.Error("initialize child", "server", name, "err", err)
					continue
				}
				d.clients[prefix] = client
				d.agg.AddServer(prefix, client)
				if err := d.agg.RefreshAll(ctx); err != nil {
					d.Logger.Error("refresh", "err", err)
				}
			}
			// If the process is not yet up, we'll re-attach on next reconcile
			// (the supervisor crash goroutine will wake us when it transitions).
		}
	}
	// Remove anything not wanted.
	for prefix := range d.clients {
		if _, keep := wanted[prefix]; !keep {
			d.agg.RemoveServer(prefix)
			delete(d.clients, prefix)
			// Supervisor: find server by matching prefix → name mapping.
			for name, s := range cfg.McpServers {
				if config.EffectivePrefix(name, s) == prefix {
					d.sup.Remove(name)
				}
			}
		}
	}
}
```

> **Note for implementer:** this `reconcile` has a known limitation — if a process is still starting when reconcile runs, the client attach is skipped and we don't retry until the next reconcile cycle. Task 6.3 addresses this with a "client attach loop" that watches supervisor status.

- [ ] **Step 2:** Run a compile-only check:

```bash
go build ./internal/daemon/...
```

Expected: no errors.

- [ ] **Step 3:** Commit:

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): wire config + supervisor + aggregator + HTTP"
```

### Task 6.2: Daemon subcommand wired

**Files:**
- Modify: `cmd/mcp-gateway/main.go:14-19` (replace `newDaemonCmd` body)

- [ ] **Step 1:** Replace the `newDaemonCmd` function in `cmd/mcp-gateway/main.go` with:

```go
func newDaemonCmd() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the mcp-gateway daemon (long-running)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if home == "" {
				h, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				home = filepath.Join(h, ".mcp-gateway")
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			d := daemon.New(home, logger)

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				cancel()
			}()
			return d.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&home, "home", "", "path to ~/.mcp-gateway (default: $HOME/.mcp-gateway)")
	return cmd
}
```

- [ ] **Step 2:** Add the necessary imports to the top of `main.go`:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ayushraj/mcp-gateway/internal/daemon"
	"github.com/spf13/cobra"
)
```

(Remove any duplicate/unused imports.)

- [ ] **Step 3:** Build and smoke test with an invalid config:

```bash
make build
mkdir -p /tmp/mgw-smoke
./bin/mcp-gateway daemon --home /tmp/mgw-smoke
```

Expected: exits with "config not found at /tmp/mgw-smoke/config.jsonc".

- [ ] **Step 4:** Smoke test with a minimal valid config (prints startup log, then Ctrl-C to exit):

```bash
cat > /tmp/mgw-smoke/config.jsonc <<'EOF'
{
  "version": 1,
  "daemon": { "http_port": 17823, "log_level": "info" },
  "mcpServers": {}
}
EOF
./bin/mcp-gateway daemon --home /tmp/mgw-smoke &
GATEWAY_PID=$!
sleep 1
curl -s -X POST http://127.0.0.1:17823/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":"1","method":"tools/list"}'
kill $GATEWAY_PID
wait $GATEWAY_PID 2>/dev/null || true
```

Expected: the curl returns `{"jsonrpc":"2.0","id":"1","result":{"tools":[]}}`. Daemon exits cleanly.

- [ ] **Step 5:** Commit:

```bash
git add cmd/mcp-gateway/main.go
git commit -m "feat(cli): wire 'daemon' subcommand to internal/daemon.Run"
```

### Task 6.3: Retry client attach when process becomes ready

> **Problem:** reconcile runs before the supervisor has spawned the child; client attach fails; we don't retry. **Fix:** periodically poll supervisor statuses and attach clients for any `running` server that doesn't yet have a client.

**Files:**
- Modify: `internal/daemon/daemon.go:Run` (add a poll goroutine)

- [ ] **Step 1:** In `internal/daemon/daemon.go`, add this helper at the bottom of the file:

```go
// attachLoop re-attaches clients for servers that the supervisor has transitioned
// into StateRunning since the last reconcile. Runs until ctx is cancelled.
func (d *Daemon) attachLoop(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.reattach(ctx)
		}
	}
}

func (d *Daemon) reattach(ctx context.Context) {
	d.mu.Lock()
	cfg := d.cfg
	if cfg == nil {
		d.mu.Unlock()
		return
	}
	sup := d.sup
	agg := d.agg
	// Snapshot names that are configured+enabled and whose process is running
	// but don't yet have a client.
	type want struct{ name, prefix string }
	var toAttach []want
	for name, s := range cfg.McpServers {
		if !s.Enabled {
			continue
		}
		prefix := config.EffectivePrefix(name, s)
		if _, has := d.clients[prefix]; has {
			continue
		}
		if sup.Status(name).State != supervisor.StateRunning {
			continue
		}
		toAttach = append(toAttach, want{name, prefix})
	}
	d.mu.Unlock()

	for _, w := range toAttach {
		p := sup.Process(w.name)
		if p == nil {
			continue
		}
		client := mcpchild.New(w.name, p.Stdin, p.Stdout)
		ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := client.Initialize(ictx); err != nil {
			cancel()
			d.Logger.Warn("attach: initialize failed", "server", w.name, "err", err)
			continue
		}
		cancel()

		d.mu.Lock()
		// Double-check nobody beat us to it.
		if _, exists := d.clients[w.prefix]; exists {
			d.mu.Unlock()
			continue
		}
		d.clients[w.prefix] = client
		d.mu.Unlock()
		agg.AddServer(w.prefix, client)
		if err := agg.RefreshAll(ctx); err != nil {
			d.Logger.Warn("attach: refresh failed", "server", w.name, "err", err)
		} else {
			d.Logger.Info("attached", "server", w.name, "prefix", w.prefix)
		}
	}
}
```

- [ ] **Step 2:** In the `Run` method, after the `go d.sup.Run(ctx)` line, add:

```go
go d.attachLoop(ctx)
```

- [ ] **Step 3:** Run tests + build:

```bash
go test ./internal/daemon/ -race -v
make build
```

Expected: pass, binary builds.

- [ ] **Step 4:** Commit:

```bash
git add internal/daemon/daemon.go
git commit -m "feat(daemon): attach loop retries client init as children become ready"
```

### Task 6.4: End-to-end test using the real binary and fakechild

> **Goal:** prove that writing a config, starting the daemon binary, and hitting the HTTP endpoint returns tools from a real supervised child. Uses the `fakechild` package built into a temporary binary for the test.

**Files:**
- Create: `internal/testutil/fakechild/cmd/fakechildbin/main.go`
- Create: `internal/daemon/e2e_test.go`

- [ ] **Step 1:** Create a small main that exposes `fakechild.Server` as a real binary. `internal/testutil/fakechild/cmd/fakechildbin/main.go`:

```go
package main

import (
	"encoding/json"
	"os"

	"github.com/ayushraj/mcp-gateway/internal/testutil/fakechild"
)

func main() {
	tools := []fakechild.Tool{
		{Name: "ping", Description: "returns pong", InputSchema: fakechild.MustRaw(map[string]any{"type": "object"})},
	}
	srv := fakechild.New(tools, func(name string, _ json.RawMessage) ([]any, bool) {
		return []any{fakechild.StringContent("pong")}, false
	})
	_ = srv.Serve(os.Stdin, os.Stdout)
}
```

- [ ] **Step 2:** Create `internal/daemon/e2e_test.go` (guarded by `//go:build e2e` so regular `go test` doesn't run it):

```go
//go:build e2e

package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_DaemonServesToolsFromChild(t *testing.T) {
	// 1. Build the fakechild binary.
	tmp := t.TempDir()
	childBin := filepath.Join(tmp, "fakechild")
	out, err := exec.Command("go", "build", "-o", childBin, "./internal/testutil/fakechild/cmd/fakechildbin").CombinedOutput()
	require.NoError(t, err, "go build fakechild: %s", string(out))

	// 2. Build the gateway binary.
	gatewayBin := filepath.Join(tmp, "mcp-gateway")
	out, err = exec.Command("go", "build", "-o", gatewayBin, "./cmd/mcp-gateway").CombinedOutput()
	require.NoError(t, err, "go build gateway: %s", string(out))

	// 3. Write config pointing at the fakechild.
	home := filepath.Join(tmp, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	cfg := fmt.Sprintf(`{
		"version": 1,
		"daemon": { "http_port": 17902, "log_level": "info" },
		"mcpServers": {
			"fake": { "command": %q, "enabled": true }
		}
	}`, childBin)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.jsonc"), []byte(cfg), 0o600))

	// 4. Start the daemon.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gatewayBin, "daemon", "--home", home)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// 5. Poll /mcp until tools/list is non-empty (child takes a moment to init).
	var tools []any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body := strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"tools/list"}`)
		req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:17902/mcp", body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var r struct {
				Result struct {
					Tools []any `json:"tools"`
				} `json:"result"`
			}
			if json.Unmarshal(b, &r) == nil && len(r.Result.Tools) > 0 {
				tools = r.Result.Tools
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, tools, "tools/list returned empty after timeout")
	first := tools[0].(map[string]any)
	assert.Equal(t, "fake__ping", first["name"])

	// 6. Call the tool.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "2", "method": "tools/call",
		"params": map[string]any{"name": "fake__ping", "arguments": map[string]any{}},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:17902/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var r struct {
		Result struct {
			Content []map[string]any `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(b, &r))
	require.Len(t, r.Result.Content, 1)
	assert.Equal(t, "pong", r.Result.Content[0]["text"])
}
```

- [ ] **Step 3:** Run the e2e test with the build tag:

```bash
make e2e
```

Expected: PASS. (If you see port-in-use errors, switch to port `0` and read the actual port from daemon stderr — for v0 we just keep a high static port.)

- [ ] **Step 4:** Commit:

```bash
git add internal/testutil/fakechild/cmd/fakechildbin/main.go internal/daemon/e2e_test.go
git commit -m "test(daemon): e2e — real binary spawns child and serves tools/list + tools/call"
```

---

## Phase 7 — stdio bridge + status subcommand

### Task 7.1: stdio bridge — write the test first

**Files:**
- Create: `internal/bridge/bridge_test.go`

- [ ] **Step 1:** Create `internal/bridge/bridge_test.go`:

```go
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHTTP returns a test HTTP server that echoes JSON-RPC with id+result.
func fakeHTTP() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"method": req.Method, "ok": true},
		})
	})
	return httptest.NewServer(mux)
}

func TestBridge_ProxiesOneRequestResponse(t *testing.T) {
	srv := fakeHTTP()
	defer srv.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"initialize"}` + "\n")
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Run(ctx, RunConfig{
		URL:    srv.URL + "/mcp",
		Stdin:  in,
		Stdout: &out,
	})
	require.NoError(t, err)
	line, err := readLine(&out)
	require.NoError(t, err)
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &resp))
	assert.Equal(t, "1", string(resp["id"].(string)))
	result := resp["result"].(map[string]any)
	assert.Equal(t, "initialize", result["method"])
	assert.Equal(t, true, result["ok"])
}

func readLine(r io.Reader) (string, error) {
	var buf bytes.Buffer
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				return buf.String(), nil
			}
			buf.WriteByte(b[0])
		}
		if err != nil {
			return buf.String(), err
		}
	}
}
```

> Note: ids may come back as strings or numbers depending on encoding; adjust the assertion if needed.

- [ ] **Step 2:** Run — fails, `Run` and `RunConfig` undefined.

### Task 7.2: stdio bridge — implement

**Files:**
- Create: `internal/bridge/bridge.go`

- [ ] **Step 1:** Create `internal/bridge/bridge.go`:

```go
// Package bridge implements a thin stdio ↔ HTTP proxy so that stdio-only MCP
// clients (e.g. Claude Desktop) can talk to the mcp-gateway daemon's Streamable
// HTTP endpoint. Each newline-delimited JSON frame on stdin becomes a single
// HTTP POST; each response body becomes one newline-delimited JSON frame on
// stdout. Streams (SSE) are v1.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RunConfig parameterises the bridge.
type RunConfig struct {
	URL    string    // full URL to the daemon's /mcp endpoint
	Stdin  io.Reader // where client writes requests
	Stdout io.Writer // where we write responses
}

// Run reads frames from Stdin and proxies to URL; writes each response to Stdout.
// Returns when Stdin hits EOF or ctx is cancelled.
func Run(ctx context.Context, cfg RunConfig) error {
	if cfg.URL == "" {
		return errors.New("bridge: URL required")
	}
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		return fmt.Errorf("bridge: URL must be http(s)://, got %s", cfg.URL)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	scanner := bufio.NewScanner(cfg.Stdin)
	// MCP frames can exceed the default 64KB; raise the buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(line))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		// Ensure exactly one newline at the end.
		body = bytes.TrimRight(body, "\r\n")
		body = append(body, '\n')
		if _, werr := cfg.Stdout.Write(body); werr != nil {
			return werr
		}
	}
	return scanner.Err()
}
```

- [ ] **Step 2:** Run tests:

```bash
go test ./internal/bridge/ -race -v
```

Expected: PASS.

- [ ] **Step 3:** Commit:

```bash
git add internal/bridge/
git commit -m "feat(bridge): newline-framed stdio ↔ HTTP proxy"
```

### Task 7.3: stdio subcommand wired

**Files:**
- Modify: `cmd/mcp-gateway/main.go` (replace `newStdioCmd` body)

- [ ] **Step 1:** Replace `newStdioCmd` with:

```go
func newStdioCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "stdio",
		Short: "Run as a stdio bridge to the local daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
			return bridge.Run(cmd.Context(), bridge.RunConfig{
				URL:    url,
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
			})
		},
	}
	cmd.Flags().IntVar(&port, "port", 7823, "daemon HTTP port")
	return cmd
}
```

- [ ] **Step 2:** Add the bridge import to `main.go`:

```go
"github.com/ayushraj/mcp-gateway/internal/bridge"
```

- [ ] **Step 3:** Build:

```bash
make build
```

- [ ] **Step 4:** Smoke test (requires the daemon running from Task 6.2 smoke, port 17823; adjust `--port`):

Start the daemon in one terminal:

```bash
./bin/mcp-gateway daemon --home /tmp/mgw-smoke
```

In another terminal:

```bash
echo '{"jsonrpc":"2.0","id":"1","method":"tools/list"}' | ./bin/mcp-gateway stdio --port 17823
```

Expected: a single JSON line printed: `{"jsonrpc":"2.0","id":"1","result":{"tools":[]}}`.

- [ ] **Step 5:** Commit:

```bash
git add cmd/mcp-gateway/main.go
git commit -m "feat(cli): wire 'stdio' subcommand to internal/bridge"
```

### Task 7.4: status subcommand

**Files:**
- Modify: `cmd/mcp-gateway/main.go` (replace `newStatusCmd`)

- [ ] **Step 1:** Replace `newStatusCmd` with:

```go
func newStatusCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print daemon status (hits /mcp initialize)",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
			req := []byte(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"status","version":"0"}}}`)
			r, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, url, bytes.NewReader(req))
			if err != nil {
				return err
			}
			r.Header.Set("Content-Type", "application/json")
			cli := &http.Client{Timeout: 2 * time.Second}
			resp, err := cli.Do(r)
			if err != nil {
				return fmt.Errorf("daemon unreachable at %s: %w", url, err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("daemon: OK (port %d)\n%s\n", port, string(body))
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 7823, "daemon HTTP port")
	return cmd
}
```

- [ ] **Step 2:** Add missing imports: `bytes`, `io`, `net/http`, `time`.

- [ ] **Step 3:** Build and smoke:

```bash
make build
./bin/mcp-gateway status --port 17823  # requires daemon from earlier smoke
```

Expected: `daemon: OK (port 17823)` + the raw initialize response.

- [ ] **Step 4:** Commit:

```bash
git add cmd/mcp-gateway/main.go
git commit -m "feat(cli): add 'status' subcommand that pings /mcp initialize"
```

---

## Phase 8 — Final pass

### Task 8.1: Lint clean

- [ ] **Step 1:** Install `golangci-lint` if missing (`brew install golangci-lint` or see https://golangci-lint.run).

- [ ] **Step 2:** Run:

```bash
make lint
```

Expected: no issues. If there are issues, fix them inline (usually one of: unused imports, shadowed vars, or `errcheck` complaints).

- [ ] **Step 3:** Commit any fixes:

```bash
git add -A
git commit -m "style: resolve golangci-lint findings"
```

### Task 8.2: All tests pass with race detector

- [ ] **Step 1:** Run:

```bash
go test -race -count=1 ./...
```

Expected: all PASS.

- [ ] **Step 2:** Run e2e:

```bash
make e2e
```

Expected: PASS.

### Task 8.3: Tag a v0.1 alpha

- [ ] **Step 1:** Ensure the working tree is clean:

```bash
git status
```

Expected: `nothing to commit, working tree clean`.

- [ ] **Step 2:** Tag:

```bash
git tag v0.1.0-alpha
```

- [ ] **Step 3:** (No push in this plan — Plan 03 covers the release pipeline.)

---

## v0.1 acceptance checklist

Before considering Plan 01 done, confirm by hand:

- [ ] `make build` produces `bin/mcp-gateway`.
- [ ] `./bin/mcp-gateway --help` shows `daemon`, `stdio`, `status`.
- [ ] `./bin/mcp-gateway daemon --home /tmp/mgw-smoke` starts and logs `mcp-gateway listening ... addr=127.0.0.1:<port>`.
- [ ] With a config containing one real MCP server (e.g. `@modelcontextprotocol/server-filesystem`), the daemon spawns it, logs `attached server=... prefix=...`, and `tools/list` returns non-empty prefixed tools.
- [ ] Editing the config file to toggle `enabled: false` causes the tool to disappear from `tools/list` within a few seconds (hot reload).
- [ ] `./bin/mcp-gateway stdio --port <same>` proxies a `tools/list` line through to the daemon and back.
- [ ] `./bin/mcp-gateway status --port <same>` prints a daemon OK response.
- [ ] `go test -race -count=1 ./...` passes.
- [ ] `make e2e` passes.
- [ ] `make lint` clean.
- [ ] Daemon shuts down cleanly on SIGINT/SIGTERM (`ps` shows no orphaned children).

---

## Known carry-overs into Plan 02 / 03

- No TUI yet.
- No event bus / token estimator yet (both blocked by TUI design).
- No admin RPC on UNIX socket yet (stdio bridge uses loopback TCP).
- No secret resolution (`${secret:*}` currently passed through as literal string — will be added with the secret resolver).
- No `add`/`rm`/`enable`/`disable`/`secret` CLI subcommands.
- No pidfile/lockfile, no launchd plist.
- No Homebrew / goreleaser / release pipeline.
- Streamable HTTP only implements POST half; server-initiated SSE stream not supported.
- Downstream transport is stdio only (no HTTP/SSE child servers).
- **Progress notifications & upstream-initiated cancellation are not routed across the gateway.** Client cancellation via HTTP ctx cancel works (the POST aborts); but `notifications/cancelled` from the upstream client is not forwarded to the owning downstream child. Wire this in Plan 02 alongside the event bus (needs request-ID ↔ downstream mapping, which the event bus model makes natural).
- Sampling / elicitation not forwarded.
- macOS + Linux only (build-tested); Windows deferred.
