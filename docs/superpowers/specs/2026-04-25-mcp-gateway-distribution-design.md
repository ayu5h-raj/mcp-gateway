# mcp-gateway v1.0.0 — Distribution Design Spec

**Status:** Draft v1 — approved scope, pending implementation
**Date:** 2026-04-25
**Owner:** ayushraj
**Targets:** v1.0.0 release

---

## 1. Overview

v0.3 shipped a feature-complete daemon + TUI. The remaining barrier to v1.0.0 is **distribution**: today the project requires `git clone && make build`, which is the entire reason the wider MCP community can't use it. v1.0.0 closes that gap.

The bar for v1.0.0 is:

> A user with macOS or Linux who has never heard of mcp-gateway can go from "saw a tweet about it" to "running with my Claude Desktop / Cursor servers migrated and the daemon auto-starting on login" in a single terminal session of five commands or fewer.

### What's already done (no changes needed)

- Daemon, supervisor, aggregator, mcpchild, stdio bridge (v0.1).
- Pidfile, event bus, admin RPC, mutation CLI, env-resolver (v0.2).
- TUI with 3 tabs, server detail, polish (v0.3).

### What this plan adds

1. **`mcp-gateway init`** — a first-run wizard that detects existing MCP client configs, migrates their servers into mcp-gateway, patches the client to point at the gateway, and optionally installs the launchd auto-start service. End-state: the user runs their MCP client and everything works.
2. **`mcp-gateway service install | uninstall | status`** — manages a launchd plist at `~/Library/LaunchAgents/com.ayu5h-raj.mcp-gateway.plist` so the daemon survives reboots without manual intervention.
3. **Goreleaser pipeline** — tag-driven release workflow that builds darwin arm64/amd64 and linux arm64/amd64 binaries, uploads them as a GitHub Release, and bumps the Homebrew formula in the existing tap.
4. **Homebrew formula** — `Formula/mcp-gateway.rb` in the existing `github.com/ayu5h-raj/homebrew-tap` repo. Auto-bumped by goreleaser.
5. **`install.sh`** — POSIX shell installer for non-brew users (Linux mostly, macOS optional). Detects OS+arch, downloads the correct tarball from the GitHub Release, verifies SHA256, installs to `/usr/local/bin` with `~/.local/bin` fallback.
6. **README rewrite** — Quick Start collapses to one `brew install` + one `mcp-gateway init` command. Install methods, uninstall, service management documented.

### Explicit non-goals (deferred to post-v1.0.0)

| Deferred | Why |
|---|---|
| **macOS code signing / notarization** | Requires an Apple Developer account ($99/yr) and a notarization workflow. `brew` users won't see a Gatekeeper warning (brew owns trust); `install.sh` users see "unidentified developer" once and can right-click→Open. Document in README; revisit in v1.1 if friction is reported. |
| **In-binary auto-updater** | `brew upgrade` and re-running `install.sh` cover this. Self-updaters are a security and UX rabbit hole (signature verification, rollback on failure, atomic swap). |
| **Windows support** | Different service manager (Service Control Manager), different config paths, no `~/.mcp-gateway` convention, different shell. Plan 05+. |
| **Linux systemd unit** | Linux v1.0 ships the binary + `mcp-gateway start`. The systemd unit is one file but should be authored and tested on a real Linux machine; defer to v1.1. |
| **`init` wizard as TUI** | Plain prompts (Y/n) keep the wizard small, scriptable (`yes | mcp-gateway init`), and identical-feeling across terminals. A Bubble Tea wizard is a nice-to-have for v1.1 if onboarding telemetry shows people getting stuck. |

---

## 2. User experience walkthroughs

### 2.1 The happy path — first-time user with existing Claude Desktop config

```
$ brew install ayu5h-raj/tap/mcp-gateway
$ mcp-gateway init

mcp-gateway init — first-run wizard

Detected MCP clients:
  • Claude Desktop  (~/Library/Application Support/Claude/claude_desktop_config.json)
      kite        — npx mcp-remote https://mcp.kite.trade/sse
      filesystem  — npx -y @modelcontextprotocol/server-filesystem /Users/ayushraj
  • Cursor          (~/.cursor/mcp.json)
      (no servers configured)

Import 2 servers from Claude Desktop into mcp-gateway? [Y/n] y
  ✓ wrote ~/.mcp-gateway/config.jsonc

Patch Claude Desktop config to point at the gateway? [Y/n] y
  ✓ backed up to ~/Library/Application Support/Claude/claude_desktop_config.json.bak.20260425-090112
  ✓ wrote ~/Library/Application Support/Claude/claude_desktop_config.json

Auto-start mcp-gateway on login (recommended)? [Y/n] y
  ✓ wrote ~/Library/LaunchAgents/com.ayu5h-raj.mcp-gateway.plist
  ✓ launchctl bootstrap gui/501

mcp-gateway is running. Restart Claude Desktop to pick up the new config.

Useful commands:
  mcp-gateway tui              # live ops dashboard
  mcp-gateway list             # show servers and tool counts
  mcp-gateway add <name> ...   # add a new server
  mcp-gateway service status   # check the launchd service
$
```

Five commands minus shell prompts: `brew install`, `mcp-gateway init`, three `Y` keystrokes, restart Claude Desktop. The user is done.

### 2.2 The escape hatches

- **`mcp-gateway init --no-import`** — write an empty config and exit. For users who want to start fresh.
- **`mcp-gateway init --no-patch`** — import servers but don't touch any client config; print the manual edit needed.
- **`mcp-gateway init --no-service`** — skip the launchd step.
- **`yes | mcp-gateway init`** — non-interactive: import everything, patch everything, install service. For dotfile bootstrap scripts.
- **All three `--no-*` flags + a directory in `--config`** — fully scriptable, never prompts.

If `~/.mcp-gateway/config.jsonc` already exists with non-empty `mcpServers`, `init` refuses by default and prints `mcp-gateway is already configured. Pass --force to overwrite, or use mcp-gateway add to add servers.`

### 2.3 Uninstall

```
$ mcp-gateway service uninstall      # bootout the launchd plist
$ brew uninstall mcp-gateway          # or rm /usr/local/bin/mcp-gateway
$ rm -rf ~/.mcp-gateway               # config + logs + pidfile
```

Each step is idempotent. `mcp-gateway service uninstall` on a never-installed system prints `service not installed (no-op)` and exits 0.

---

## 3. Components

### 3.1 `internal/clientcfg/` — MCP client config detector and rewriter

**Purpose.** Parse and rewrite the MCP server lists in well-known client configs (Claude Desktop, Cursor) without disturbing the rest of the file.

**Public API:**

```go
package clientcfg

// Client describes one supported MCP client.
type Client struct {
    Name       string // "Claude Desktop", "Cursor"
    ConfigPath string // absolute path on this machine
}

// KnownClients returns the list of clients we know how to read on this OS.
// On macOS:  Claude Desktop + Cursor.
// On Linux:  Claude Desktop (config.json under XDG dir) + Cursor.
func KnownClients() []Client

// Server is one downstream MCP server entry from a client config.
type Server struct {
    Name    string            // map key in mcpServers
    Command string            // "npx", "/usr/local/bin/foo"
    Args    []string
    Env     map[string]string
    Enabled bool              // not all clients have this; default true
}

// Detect returns the list of (Client, []Server) pairs whose configs exist
// and parse cleanly. Missing files are skipped silently. Parse errors are
// logged but don't fail the whole detection.
func Detect() []Detected

type Detected struct {
    Client  Client
    Servers []Server
    Err     error // nil on success; set if config exists but failed to parse
}

// Patch rewrites a client config to replace the named servers with a single
// "mcp-gateway" stdio entry pointing at the binary. Backs up the original
// to <path>.bak.<timestamp> before writing. Atomic via tmp+rename.
//
// gatewayBinary is the absolute path to mcp-gateway. It will be invoked as:
//   command: gatewayBinary
//   args:    ["stdio"]
// (The stdio bridge subcommand already exists from v0.1.)
func Patch(c Client, replacedServers []string, gatewayBinary string) error
```

**Files:**

- `internal/clientcfg/clientcfg.go` — `Client`, `Server`, `KnownClients`, `Detect`.
- `internal/clientcfg/claude_desktop.go` — Claude Desktop format reader/writer.
- `internal/clientcfg/cursor.go` — Cursor format reader/writer.
- `internal/clientcfg/clientcfg_test.go` — table-driven tests with fixtures under `testdata/`.

**Format notes:**

- **Claude Desktop:** strict JSON, top-level `{"mcpServers": {"name": {"command":"...","args":[],"env":{}}}}`. Other top-level keys (`globalShortcut`, etc.) must be preserved verbatim. We use `encoding/json` with `RawMessage` for unknown keys.
- **Cursor:** strict JSON, identical schema. Different path (`~/.cursor/mcp.json`).
- **Both:** the rewriter MUST preserve unknown keys. Tests assert this.

**Backup convention.** `<path>.bak.<YYYYMMDD-HHMMSS>`, never overwritten. If a backup with that exact timestamp exists (race with multiple inits), append `-1`, `-2`, etc.

### 3.2 `internal/service/` — launchd plist management

**Purpose.** Generate, install, uninstall, and report the status of the macOS launchd plist that auto-starts the daemon.

**Public API:**

```go
package service

// PlistPath returns the absolute path the plist would live at for the
// current user. ~/Library/LaunchAgents/com.ayu5h-raj.mcp-gateway.plist
func PlistPath() (string, error)

// Status reports whether the plist exists on disk and whether launchd
// has it loaded.
type Status struct {
    PlistInstalled bool
    LaunchdLoaded  bool
    PID            int    // 0 if not running
}

func GetStatus() (Status, error)

// Install renders the plist from a template using gatewayBinary as the
// program path, writes it (atomic tmp+rename), and runs `launchctl bootstrap`.
// Idempotent: if already installed, replaces the file and re-bootstraps.
func Install(gatewayBinary string) error

// Uninstall runs `launchctl bootout` if loaded, then removes the plist.
// Idempotent: missing plist is a no-op.
func Uninstall() error
```

**Files:**

- `internal/service/service.go` — `PlistPath`, `GetStatus`, `Install`, `Uninstall`.
- `internal/service/plist.go` — embedded template + render function. Uses `text/template` with `{{.GatewayBinary}}`, `{{.LogDir}}`, `{{.LogFile}}`.
- `internal/service/plist_template.xml` — the actual plist template, embedded with `//go:embed`.
- `internal/service/service_test.go` — render tests (golden file), platform guard tests.

**Platform support:**

- `service.go` — pure (no `launchctl` calls), testable on any platform.
- `service_darwin.go` — `Install`/`Uninstall` shell out to `launchctl bootstrap gui/$(id -u) <plist>` and `launchctl bootout gui/$(id -u) <plist>`.
- `service_other.go` (build tag `!darwin`) — all functions return `ErrUnsupported`.
- `cmd/mcp-gateway/service.go` checks `runtime.GOOS != "darwin"` early and prints a friendly message instead of calling into `service.Install`.

**Plist template** (the actual file):

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
      <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    </dict>
  </dict>
</plist>
```

`KeepAlive` — launchd respawns the daemon if it crashes. The supervisor inside the daemon respawns *child* MCP servers; launchd respawns the daemon itself.

### 3.3 `cmd/mcp-gateway/init.go` — the wizard

**Purpose.** Orchestrate detect → confirm → write → patch → service install.

**Flags:**

```
--no-import       skip detection and write an empty config
--no-patch        import but don't modify any client config
--no-service      don't install the launchd service
--force           overwrite an existing non-empty ~/.mcp-gateway/config.jsonc
--yes / -y        accept all prompts (alias for non-interactive mode)
--config <path>   override config destination (default ~/.mcp-gateway/config.jsonc)
```

**Behaviour:**

1. Resolve `gatewayBinary` via `os.Executable()`, then `filepath.EvalSymlinks` so the patched client config has the canonical path (works even if mcp-gateway later moves).
2. If `~/.mcp-gateway/config.jsonc` exists with `len(mcpServers) > 0` and `--force` is not set, error out.
3. If `--no-import` is set, write empty config and skip to step 6.
4. Call `clientcfg.Detect()`. If no clients have any servers, print "no clients detected" and write empty config.
5. For each `Detected` with servers:
   - Print a short summary.
   - Prompt `Import N servers from <client>? [Y/n]` (or auto-yes if `-y`).
   - If yes, merge the servers into the config write.
   - If `--no-patch` is not set, prompt `Patch <client>'s config? [Y/n]`. If yes, call `clientcfg.Patch`.
6. Write the merged config via `internal/configwrite` (atomic, validates first).
7. If `--no-service` is not set:
   - Prompt `Auto-start on login? [Y/n]`.
   - If yes, call `service.Install(gatewayBinary)`.
8. Print the "Useful commands" footer and exit 0.

**Prompt helper.** Lives at `cmd/mcp-gateway/prompt.go` — wraps `bufio.NewReader(os.Stdin)`, supports `-y` global, prints `[Y/n]` (default yes) or `[y/N]` (default no). Returns false if `os.Stdin` isn't a TTY and no `-y` was passed (so piped runs without `-y` get safe defaults).

**Atomicity.** Each step that mutates a file uses tmp+rename. If `Patch` fails after writing the mcp-gateway config, the user's client configs are still intact (we patch *after* writing our own). If `Install` fails after `Patch`, the user can re-run `init` or `mcp-gateway service install` later.

### 3.4 `cmd/mcp-gateway/service.go` — service subcommand

```
mcp-gateway service install    # generate plist + bootstrap
mcp-gateway service uninstall  # bootout + remove plist
mcp-gateway service status     # report Status struct
```

`status` output:

```
service:        installed
launchd:        loaded (pid 12345)
plist:          /Users/you/Library/LaunchAgents/com.ayu5h-raj.mcp-gateway.plist
log:            /Users/you/.mcp-gateway/daemon.log
```

Or:

```
service:        not installed
```

Or on Linux:

```
service: macOS only — on Linux, run `mcp-gateway start` from your shell rc.
         systemd unit support is planned for v1.1.
```

### 3.5 Goreleaser config

**File:** `.goreleaser.yaml` at the repo root.

**Builds:**
- `darwin/arm64`, `darwin/amd64`, `linux/arm64`, `linux/amd64`
- Build flags: `-trimpath -ldflags="-s -w -X main.version={{.Version}} -X main.commit={{.ShortCommit}} -X main.date={{.Date}}"`
- Two binaries: `mcp-gateway` and `mgw-smoke` (the smoke client is a useful diagnostic tool).

**Archives:**
- Naming: `mcp-gateway-{{.Os}}-{{.Arch}}-{{.Version}}.tar.gz` — matches the existing tap naming convention (`debris-macos-arm64-v0.3.0.tar.gz`). The tap formula already follows this pattern.
- Replacements: `darwin → macos`, `amd64 → intel`. So the artifact is `mcp-gateway-macos-arm64-v1.0.0.tar.gz`.
- Includes: the binary, `LICENSE`, `README.md`.

**Checksums:** `mcp-gateway_v1.0.0_checksums.txt` (SHA256).

**GitHub Release:**
- Auto-generated changelog from commits since the last tag (filtered to `feat:` and `fix:` prefixes).
- Body template: include the install one-liners.

**Brew tap integration** (`brews:` section):
```yaml
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
    test: |
      system "#{bin}/mcp-gateway", "--version"
    install: |
      bin.install "mcp-gateway"
      bin.install "mgw-smoke"
```

`HOMEBREW_TAP_TOKEN` is a GitHub PAT (or fine-grained token) with `contents:write` on the `homebrew-tap` repo. Stored as a GitHub Actions secret.

**install.sh attachment:**
```yaml
release:
  extra_files:
    - glob: ./scripts/install.sh
```
`scripts/install.sh` is hand-authored (see 3.8) and queries the GitHub API for the latest release at runtime — no goreleaser template substitution, no rebuilds needed when the script changes. Goreleaser attaches it to each release as a download so users can either `curl` it from `main` (always latest version of the script) or pin to a release URL.

### 3.6 GitHub Actions workflow

**File:** `.github/workflows/release.yaml`

**Trigger:** push of a tag matching `v*.*.*`.

**Steps:**
1. Checkout (fetch-depth 0).
2. Set up Go 1.25.
3. Set up GoReleaser (`goreleaser/goreleaser-action@v6`).
4. Run `goreleaser release --clean`.
5. Env: `GITHUB_TOKEN` (default), `HOMEBREW_TAP_TOKEN` (secret).

**Existing CI** (`.github/workflows/ci.yaml`) is untouched — still runs `make test && go vet && lint` on every push/PR.

### 3.7 Homebrew formula

**Path:** `homebrew-tap/Formula/mcp-gateway.rb` (in the existing `github.com/ayu5h-raj/homebrew-tap` repo).

**Generated by goreleaser**, but the first version we'll commit by hand to validate the format:

```ruby
class McpGateway < Formula
  desc "Local-first MCP aggregator — k9s for MCP"
  homepage "https://github.com/ayu5h-raj/mcp-gateway"
  version "1.0.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-macos-arm64-v1.0.0.tar.gz"
      sha256 "<filled by goreleaser>"
    end
    on_intel do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-macos-intel-v1.0.0.tar.gz"
      sha256 "<filled by goreleaser>"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-linux-arm64-v1.0.0.tar.gz"
      sha256 "<filled by goreleaser>"
    end
    on_intel do
      url "https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/mcp-gateway-linux-intel-v1.0.0.tar.gz"
      sha256 "<filled by goreleaser>"
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

Install command: `brew install ayu5h-raj/tap/mcp-gateway`.

### 3.8 install.sh

**Path:** `scripts/install.sh` (committed in this repo, also attached to each release as a download).

**Behaviour:**
1. `set -euo pipefail`.
2. Detect OS (`uname -s` → `darwin`/`linux`) and arch (`uname -m` → `arm64`/`x86_64` → `intel`).
3. Query the GitHub API for the latest release tag (or accept `MCP_GATEWAY_VERSION` env var to pin).
4. Download `mcp-gateway-<os>-<arch>-<version>.tar.gz` and the checksums file.
5. Verify SHA256.
6. Untar to a temp dir, move `mcp-gateway` and `mgw-smoke` to `/usr/local/bin` (sudo if needed) or `~/.local/bin` (auto-fallback if `/usr/local/bin` isn't writable and we're not root).
7. Print the post-install instructions: run `mcp-gateway init`.

**Used as:**
```sh
curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
```

Or pinned to a release:
```sh
MCP_GATEWAY_VERSION=v1.0.0 curl -fsSL https://github.com/ayu5h-raj/mcp-gateway/releases/download/v1.0.0/install.sh | sh
```

**Tests:** `bash -n scripts/install.sh` (parse-check) in CI. End-to-end test runs in a Docker container during the release smoke-check (Phase 8).

### 3.9 README rewrite

The current README has install instructions that are out of date. New structure:

- **Quick Start** — `brew install ayu5h-raj/tap/mcp-gateway && mcp-gateway init`. Done.
- **Other install methods** — `install.sh`, build from source. Each as a single block.
- **Uninstall** — three lines (service uninstall, brew uninstall, rm -rf).
- **Service management** — `service install/uninstall/status` reference.
- **Daily use** — `mcp-gateway tui`, `add`, `list`, `disable`, `enable`. (Already exists, keep.)
- **Config schema** — link to `internal/config/config.go` for the source of truth.
- **Troubleshooting** — Gatekeeper warning on first install.sh run, daemon log location, where to file issues.

The "What is mcp-gateway / why" section at the top of the existing README stays.

---

## 4. Security and trust

- **macOS Gatekeeper** — install.sh users will see "cannot be opened because the developer cannot be verified" on first run. Documented workaround: `xattr -d com.apple.quarantine /usr/local/bin/mcp-gateway`. Brew users are unaffected. We do not sign or notarize for v1.0.
- **HOMEBREW_TAP_TOKEN scope** — fine-grained PAT with `contents:write` on `homebrew-tap` only. Rotate annually. Documented in `docs/release-runbook.md` (new file, Phase 8).
- **install.sh integrity** — hosted on GitHub (raw.githubusercontent.com); HTTPS only. Verifies SHA256 of the downloaded tarball against the checksums file. Does not currently verify GPG signature on the checksums file itself; that's a v1.1 add.
- **Plist permissions** — written with mode 0644 (launchd requires user-readable). User-owned, no setuid.

---

## 5. Backwards compatibility

- **No config schema changes.** v1.0.0 reads the same `~/.mcp-gateway/config.jsonc` that v0.3.x writes. `init --force` overwrites; explicit user action.
- **CLI flags.** No existing flags change semantics. `service` and `init` are new top-level subcommands; nothing collides.
- **Pidfile / socket paths.** Unchanged. A v0.3 daemon and a v1.0 daemon are interoperable on the same install (you can `brew upgrade` without restarting).
- **launchd label.** `com.ayu5h-raj.mcp-gateway`. Locked in for v1.0; renaming later is a breaking change for installed users.

---

## 6. Verification

### 6.1 Unit and integration tests

| Package | Tests |
|---|---|
| `internal/clientcfg` | parse Claude Desktop fixture, parse Cursor fixture, malformed JSON returns wrapped error, Patch preserves unknown top-level keys, Patch is atomic (kill mid-write leaves original intact) |
| `internal/service` | render template golden file, `Install`/`Uninstall` on Linux returns `ErrUnsupported`, `Status` parses `launchctl print` output |
| `cmd/mcp-gateway/init` | non-interactive (`-y`) full path, `--no-import` path, `--no-patch` path, `--no-service` path, refuses on existing config without `--force` |

### 6.2 Release dry run (Phase 8)

Tag `v1.0.0-rc1`, push, watch CI:
- All four binaries built.
- Tarballs uploaded to the GitHub Release.
- Checksums file uploaded.
- Brew tap PR opened (or commit landed) on `homebrew-tap`.
- `brew install ayu5h-raj/tap/mcp-gateway` from the rc1 tag works.
- `install.sh` from the rc1 release downloads, verifies, installs.

If clean: tag `v1.0.0`. The rc tag is left in place as historical reference.

### 6.3 Manual end-to-end

```sh
# Fresh user simulation
brew uninstall mcp-gateway
rm -rf ~/.mcp-gateway

brew install ayu5h-raj/tap/mcp-gateway
mcp-gateway init -y                                    # imports + patches + installs service
launchctl print gui/$(id -u)/com.ayu5h-raj.mcp-gateway # service is loaded
ls ~/.mcp-gateway/                                     # config + sock + log + pid
mcp-gateway status                                     # daemon: OK, servers: N
mcp-gateway tui                                        # tabs render, scroll works

# Restart laptop simulation (manual)
sudo killall mcp-gateway
sleep 5
mcp-gateway status                                     # daemon: OK (launchd respawned it)

# Uninstall
mcp-gateway service uninstall
brew uninstall mcp-gateway
rm -rf ~/.mcp-gateway
```

---

## 7. Phase breakdown for Sonnet agent dispatch

Each phase is a separate Sonnet agent dispatch (per `superpowers:subagent-driven-development`). Spec compliance review + code quality review after each. Phases 1–3 are independent and can ship without any release; Phases 4–6 require GitHub access; Phase 7 follows; Phase 8 is the release.

| # | Phase | Files | Notes |
|---|---|---|---|
| 1 | **`internal/clientcfg`** | clientcfg.go, claude_desktop.go, cursor.go, clientcfg_test.go, testdata/ | Pure Go. No external deps beyond stdlib + `github.com/tidwall/jsonc` (already used). |
| 2 | **`internal/service` + `cmd/mcp-gateway/service.go`** | service.go, service_darwin.go, service_other.go, plist.go, plist_template.xml, service_test.go, cmd subcommand | macOS-only `launchctl` shellouts; build-tagged for portability. |
| 3 | **`cmd/mcp-gateway/init.go` + `cmd/mcp-gateway/prompt.go`** | init.go, prompt.go, init_test.go | Wires Phase 1 + Phase 2 + existing `internal/configwrite`. |
| 4 | **Goreleaser + GitHub Actions** | `.goreleaser.yaml`, `.github/workflows/release.yaml` | Validate locally with `goreleaser release --snapshot --clean` before pushing. |
| 5 | **Homebrew formula** | `~/Documents/github/homebrew-tap/Formula/mcp-gateway.rb` (separate repo, separate commit + push) | First version hand-written to a placeholder version; subsequent versions auto-generated. |
| 6 | **`scripts/install.sh`** | scripts/install.sh, parse-check in CI | POSIX sh, not bash. Test in `alpine:latest` and `ubuntu:latest` Docker containers. |
| 7 | **README + uninstall + troubleshooting** | README.md, plus a tiny `docs/release-runbook.md` | The runbook is for *me* — how to cut a release, rotate tokens. |
| 8 | **Release dry run + tag v1.0.0** | tag `v1.0.0-rc1`, verify, then `v1.0.0` | Manual. Updates `CHANGELOG.md` (new file) on the way. |

Phases 1, 2, and 4 are roughly independent and can be dispatched in parallel as separate Sonnet agents (no cross-file dependencies). Phase 3 depends on 1 and 2. Phase 5 depends on 4. Phase 8 depends on everything else.

---

## 8. Open questions

None blocking. The following are intentionally left to the implementer's judgment:

- **Prompt library.** I'd lean on plain `bufio.Reader` over a third-party prompt library (we already have Bubble Tea but a TUI for `init` is overkill — see non-goal 1.5). The implementer can use `github.com/charmbracelet/huh` if it noticeably simplifies the code, but YAGNI.
- **install.sh: detect existing install.** If `/usr/local/bin/mcp-gateway` already exists, prompt before overwriting? Probably yes; trivial addition.
- **changelog generation.** Goreleaser's auto-changelog is fine for v1.0; if it's noisy we'll hand-edit the release body.

---

## 9. What's next (post-v1.0.0)

- **v1.1 — Distribution polish.** Linux systemd unit. macOS notarization. install.sh GPG-signed checksums. `mcp-gateway upgrade` self-updater (maybe).
- **v1.2+ — Spec hardening (Plan 05).** HTTP / SSE downstream MCP servers (so kite doesn't need `npx mcp-remote` shim). OAuth passthrough. Sampling and elicitation forwarding. Per-client tool scoping.
- **v2 — Windows.** New service manager, new config paths, new shell. Standalone plan.
