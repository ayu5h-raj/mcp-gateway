# mcp-gateway — Design Spec

**Status:** Draft v1 — approved scope, pending final review
**Date:** 2026-04-23
**Owner:** ayushraj

---

## 1. Overview

`mcp-gateway` is a **local-first, single-binary MCP aggregator** for developers. It runs as a background daemon that connects to many downstream MCP servers once, and exposes them as a single unified MCP endpoint that every MCP-capable application (Claude Desktop, Cursor, Claude Code, VS Code, Zed, Windsurf) can use — replacing N duplicate configs with one.

It is positioned as **"k9s for MCP"** — terminal-native, zero runtime dependencies, built for developers who live in the terminal.

### Positioning (what makes this different)

The MCP aggregator category already exists (MetaMCP, 1MCP, mcp-hub, mcp-proxy). This project differentiates on three dimensions:

1. **Distribution UX.** Single static Go binary, `brew install` or `curl | sh`. No Docker, no Postgres, no Node/Python runtime. Config line in Claude Desktop becomes one absolute path.
2. **Context-budget meter.** Surfaces the #1 unsolved pain (tool defs eating 72% of context windows): the TUI shows real-time estimated token cost of currently-exposed tools, per server and total, so users can see exactly what's costing context.
3. **TUI-first ops surface.** No MCP gateway today has a `k9s`/`lazygit`-quality TUI. Live request stream, toggle servers without restarting clients, tail downstream stderr, inspect tool calls inline. Configure-once is fine; *ops* is where the TUI earns its keep.

Additionally: **macOS Keychain–backed secrets** (via `zalando/go-keyring`, cross-platform ready) — no more pasting API keys into config files.

### Goals (v0)

- Single-user, local-first daemon.
- Aggregate tools + resources + prompts from N stdio-based downstream MCP servers.
- Expose one MCP endpoint via (a) localhost Streamable HTTP and (b) a stdio bridge subcommand for stdio-only clients.
- Hot-reload on config change.
- TUI for live inspection and control.
- Context-budget meter (approximate token cost per server/tool/total).
- Keychain-backed secret references in config.
- Process supervision with backoff, crash recovery, per-child stderr capture.
- macOS-first distribution (brew tap + goreleaser).

### Non-Goals (v0 — explicitly deferred)

- Multi-user / team / SaaS / web UI.
- OAuth passthrough for remote MCP servers (GitHub, Slack, Notion) — **deferred to v1**.
- Streamable HTTP / SSE downstream servers — **deferred to v1** (stdio covers ~90% of current servers).
- Per-client tool scoping (need client identification story first) — **deferred to v1**.
- Linux/Windows support — **deferred to v1**; code kept portable via `go-keyring`.
- 1Password / Infisical / Vault / sops secret backends — **deferred to v1+**.
- Sampling and elicitation forwarding (server→client requests) — **deferred to v2**.
- OpenTelemetry exports — **deferred to v2**.
- Namespaces, middlewares, per-endpoint config (MetaMCP-style) — **deferred to v2 or never**; kept out of v0 to preserve simplicity.
- Policy / approval workflows.
- Cost tracking beyond token estimation.

---

## 2. Architecture

Two logical processes, one binary, many subcommands.

```
┌──────────────────────────────────────────────────────────────────┐
│                mcp-gateway (single Go binary)                    │
│                                                                  │
│  Subcommands:                                                    │
│    mcp-gateway daemon           # long-running, one per user     │
│    mcp-gateway stdio            # thin MCP bridge for stdio-only │
│                                 #   clients (Claude Desktop)     │
│    mcp-gateway tui              # attaches to daemon's event bus │
│    mcp-gateway add   <name> ... # mutate config, ask daemon to   │
│    mcp-gateway rm    <name>     #   reconcile                    │
│    mcp-gateway list                                              │
│    mcp-gateway enable/disable                                    │
│    mcp-gateway secret set/list/rm                                │
│    mcp-gateway status           # daemon health                  │
│    mcp-gateway start/stop/restart                                │
└──────────────────────────────────────────────────────────────────┘
```

### Topology

```
 Claude Desktop ─── mcp-gateway stdio (spawned) ─┐
                                                 │ UNIX socket
                                                 │ ~/.mcp-gateway/sock
 Cursor ──┐                                      │
 VS Code ─┤─── http://127.0.0.1:7823/mcp ────────┤
 Zed ─────┘       (Streamable HTTP)              │
                                                 ▼
                         ┌───────────────────────────────────────┐
                         │       mcp-gateway daemon              │
                         │                                       │
                         │   ┌─────────────────────────────────┐ │
                         │   │ Upstream MCP server             │ │
                         │   │ (StreamableHTTP on 127.0.0.1    │ │
                         │   │  + UNIX socket for stdio bridge)│ │
                         │   └──────────────┬──────────────────┘ │
                         │                  │                    │
                         │   ┌──────────────▼────────────────┐   │
                         │   │         Aggregator            │   │
                         │   │ merged tools/resources/prompts│   │
                         │   │  with per-server prefixing    │   │
                         │   └──────────────┬────────────────┘   │
                         │                  │                    │
                         │   ┌──────────────▼────────────────┐   │
                         │   │  Session registry (upstream   │   │
                         │   │  clients, sub state, progress │   │
                         │   │  tokens)                      │   │
                         │   └──────────────┬────────────────┘   │
                         │                  │                    │
                         │   ┌──────────────▼────────────────┐   │
                         │   │    Supervisor / router        │   │
                         │   │  spawn, restart, backoff,     │   │
                         │   │  route calls to owning child  │   │
                         │   └──────────────┬────────────────┘   │
                         │                  │                    │
                         │   ┌──────────────▼────────────────┐   │
                         │   │  Event bus (ring buffer + pub)│   │
                         │   │  → TUI subscribers            │   │
                         │   │  → daemon.log                 │   │
                         │   └───────────────────────────────┘   │
                         │                                       │
                         │   Token estimator  Config watcher     │
                         │   Secret resolver  (fsnotify)         │
                         └──────────────────┬────────────────────┘
                                            │
                            stdio ┌─────────┼──────────┐ stdio
                                  ▼         ▼          ▼
                              github     filesystem   slack
                             (child)     (child)      (child)
```

### Key architectural choices

1. **Daemon is the brain; stdio-bridge is dumb.** The `stdio` subcommand is a trivial proxy that forwards MCP frames to the UNIX socket. No config, no logic, near-zero memory and startup cost.

2. **Daemon serves HTTP on two listeners** — TCP (127.0.0.1:PORT, for modern clients) and the UNIX socket (for stdio bridge + admin RPC). Paths:
   - `POST /mcp` and `GET /mcp` — Streamable HTTP MCP endpoint (same on both listeners).
   - `/admin/*` — JSON admin RPC used by the TUI and CLI (event subscription, server state, config mutations). **Only exposed on the UNIX socket, never on the TCP listener** — keeps admin off the network even when the TCP listener is up.

   Session registry is shared across listeners.

3. **Every downstream server runs exactly once**, regardless of how many upstream clients are connected. Explicitly prevents MetaMCP's [#272](https://github.com/metatool-ai/metamcp/issues/272) memory blowup (duplicate spawns per namespace).

4. **Explicit supervisor.** Each child has a state machine: `starting → running → errored → restarting → disabled`. Exponential backoff (1s, 2s, 4s, 8s, max 60s); after N consecutive failed restarts (default 5), marked `disabled` and surfaced in TUI. No persistence of error state across daemon restarts — prevents MetaMCP [#264](https://github.com/metatool-ai/metamcp/issues/264) "stuck in ERROR".

5. **In-memory event bus.** Every MCP frame in either direction (upstream↔downstream) publishes an event. TUI subscribers read from it; nothing else touches the hot path. Ring buffer (default 10k events) gives a late-attaching TUI recent history.

6. **Config is a single JSONC file** at `~/.mcp-gateway/config.jsonc`. Hot-reloaded via `fsnotify`. No database.

7. **No multi-tenancy, no auth.** The UNIX socket is 0600; the HTTP server binds to 127.0.0.1 only. Single-user local tool.

8. **Config mutations go through the daemon.** CLI commands (`add`, `rm`, `enable`) POST to the daemon over UNIX socket; daemon updates the file atomically (write-temp + rename) and reconciles children. Prevents races between CLI writes and fsnotify reloads.

---

## 3. Data model & Config

### Config file location and format

- Path: `~/.mcp-gateway/config.jsonc` (also accepts `config.json`).
- Format: **JSONC** (JSON + `//`/`/* */` comments + trailing commas).
- Parser: strip comments/trailing commas with a small preprocessor, then `encoding/json`. Schema validation via `$schema` pointer and JSON Schema in-repo.

### Config schema (example)

```jsonc
{
  "$schema": "https://raw.githubusercontent.com/ayushraj/mcp-gateway/main/schema/config.schema.json",
  "version": 1,

  "daemon": {
    "http_port": 7823,
    "log_level": "info",
    "event_buffer_size": 10000,
    "child_restart_backoff_max_seconds": 60,
    "child_restart_max_attempts": 5
  },

  // Identical shape to Claude Desktop's mcpServers — paste blocks in directly.
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_TOKEN": "${secret:github_token}"
      },
      // mcp-gateway extensions — Claude Desktop ignores unknown fields.
      "enabled": true,
      "prefix": "github"
    },

    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/ayushraj/Documents"],
      "enabled": true
    }
  }
}
```

### Why JSONC + `mcpServers` shape

- **Migration.** Users copy-paste blocks directly from `claude_desktop_config.json` / `.vscode/mcp.json` / Cursor's `mcp.json`.
- **Comments.** JSONC gives us what YAML's only real advantage was.
- **No YAML footguns.** No Norway problem, no version-as-float, no tab/space traps. Important for a config that holds credentials.
- **Round-trip.** `encoding/json` stdlib, plus a ~40-line JSONC preprocessor. Programmatic edits via CLI stay clean.

### Secret resolution

- Syntax: `${secret:<name>}` — source-agnostic, future-proof for alternative backends.
- v0 backend: `zalando/go-keyring` (macOS Keychain in v0; cross-platform code path ready for Windows/Linux in v1).
- Resolution is **at child spawn time, in memory, injected only into that child's env**. Secrets:
  - never written to disk,
  - never logged (redacted with `[secret:NAME]` in any log),
  - never exposed to the TUI or event stream,
  - never returned from the daemon's admin API.

### Tool name prefixing

- Default: `<server>__<tool>` (double underscore).
- Rationale: matches existing Claude convention (`mcp__server__tool` in some places), distinct from Cursor's `mcp_`. Single underscore collides with conventional tool names.
- Per-server override: `"prefix": "gh"` → `gh__create_issue`.
- `"prefix": ""` is explicitly disallowed — avoids the silent-collision class of bugs ([claude-code #50319](https://github.com/anthropics/claude-code/issues/50319)).

### Token estimation

- Heuristic: `chars / 4` across `name + description + JSON schema` for each tool.
- Labeled as "~" in TUI to signal approximation.
- Sufficient to answer "which server is eating my context budget?" — the only question users actually ask.
- Pluggable internally for a future tiktoken-based backend.

### On-disk state layout

```
~/.mcp-gateway/
  config.jsonc       # user-editable, hot-reloaded
  sock               # UNIX domain socket, 0600
  daemon.pid         # flock-protected
  daemon.log         # structured (JSON) log
  servers/           # per-child stderr, size-rotated
    github.log
    filesystem.log
```

No database. No state beyond these files.

### Internal Go types (rough)

```go
type Server struct {
    Name         string
    Command      string
    Args         []string
    Env          map[string]string  // post-secret-resolution, in memory only
    Enabled      bool
    Prefix       string
    State        ServerState        // starting|running|errored|restarting|disabled
    LastError    error
    StartedAt    time.Time
    RestartCount int
}

type Tool struct {
    Server         string
    OriginalName   string
    PrefixedName   string
    Description    string
    InputSchema    json.RawMessage
    EstTokens      int
}

type Event struct {
    Time       time.Time
    Direction  Direction   // upstream-in | upstream-out | downstream-in | downstream-out
    Server     string      // if applicable
    SessionID  string      // upstream session id
    Method     string
    ID         interface{} // JSON-RPC request id
    Duration   time.Duration
    Bytes      int
    Error      string
}
```

---

## 4. MCP Protocol Behavior

### SDK

Use **`github.com/modelcontextprotocol/go-sdk`** (official Go SDK) for both upstream server and downstream client roles. Avoids reimplementing JSON-RPC, initialization handshake, streaming, and Streamable HTTP.

### Upstream session lifecycle

1. Upstream client connects (HTTP or UNIX socket).
2. Daemon returns `initialize` response advertising the **union** of downstream capabilities. Specifically:
   - `tools` if any child advertises tools.
   - `resources` (with `subscribe` if any child supports subscribe, `listChanged` if any does).
   - `prompts` (with `listChanged` if any does).
   - `logging` (always enabled; daemon forwards log/set from clients to all children that advertise it).
3. Children are lazily or eagerly initialized at daemon startup (configurable; default eager so the TUI shows useful state immediately).

### `tools/list`

- Daemon maintains a merged, cached list.
- Returned names are prefixed.
- Rebuilt when:
  - a child (re)connects and completes initialize,
  - a child emits `notifications/tools/list_changed`,
  - config is reloaded.
- On rebuild, daemon emits `notifications/tools/list_changed` to all upstream sessions.

### `tools/call`

- Parse prefix from the tool name → route to owning child.
- Unknown or disabled prefix → proper JSON-RPC error, not hang (prevents [claude-code #50319](https://github.com/anthropics/claude-code/issues/50319)).
- Progress-token bookkeeping: if the upstream request includes `progressToken`, the daemon allocates a downstream progress token, maintains mapping, forwards `notifications/progress` back to the originating session only.
- Cancellation (`notifications/cancelled` from upstream) routes to the correct downstream by matching the stored request ID.

### `resources/list`, `resources/read`, subscriptions

- Merged URI set; URIs are prefixed the same way tool names are (so `github://repos/...` becomes `github__github://repos/...` — scheme-preserving variant: prefix the opaque part after the scheme; final plan in implementation phase).
- Subscriptions (`resources/subscribe`) tracked per upstream session per URI; forwarded to correct child.
- `notifications/resources/updated` and `notifications/resources/list_changed` from children fan out to subscribing sessions only.

### `prompts/list`, `prompts/get`

- Same merge + prefix pattern as tools.

### Sampling and elicitation (server→client)

- **v0: declare unsupported.** Daemon does NOT advertise these capabilities upstream even if downstream children support them.
- **v1+:** forward sampling/elicitation requests to a chosen upstream session (policy TBD — likely "the session that originated the current tool call").

### Progress notifications

- Tracked per in-flight call. Upstream→downstream tokens rewritten to prevent collisions across sessions.

### Hot-swap semantics (config reload)

When fsnotify detects a change OR the CLI sends a reload:
- **Server added** → spawn + initialize; on success, emit `tools/list_changed` etc. to all sessions.
- **Server removed** → send `notifications/cancelled` for in-flight calls to that child; kill; emit list-changed.
- **Server command/args/env changed** → kill + respawn; emit list-changed.
- **Server enabled↔disabled flipped** → spawn or kill accordingly.
- **Daemon settings changed (port, etc.)** → warn in TUI; takes effect on next daemon restart (not hot).

### Process supervision

- Child crashed (non-zero exit or stdio EOF) → state `errored`; schedule restart with exponential backoff.
- After `child_restart_max_attempts` consecutive failures → `disabled` state, visible in TUI. User must `enable` explicitly.
- Per-child stderr captured to `servers/<name>.log` (size-rotated) and also published on the event bus for TUI tail.

### Session management (upstream side)

- Streamable HTTP: use `Mcp-Session-Id` header as session key.
- UNIX-socket stdio bridge: daemon allocates a UUID session per bridge connection.
- Per-session state: resource subscriptions, outstanding request IDs, progress token map.
- On upstream disconnect: cancel all in-flight downstream requests for that session.

---

## 5. TUI

### Framework

**Bubble Tea** (`github.com/charmbracelet/bubbletea`) + **Lipgloss** for styling + **Bubbles** for stock components (list, table, viewport, textinput). Industry standard in Go TUI (used by `gh`, `glow`, `k9s`-inspired tools).

### Attach model

`mcp-gateway tui` connects to the daemon's UNIX socket and hits `/admin/*` endpoints — separate from MCP traffic but same HTTP server. Subscribes to the event stream (`GET /admin/events` with chunked/SSE response) and polls structural state (`GET /admin/servers`, `GET /admin/tools`, `GET /admin/status`).

If the daemon is not running, the TUI offers to start it (`mcp-gateway daemon &`) and reconnect.

### Screens (tab-oriented)

1. **Dashboard** (tab `1`, default) — the k9s-style overview
   - Header: daemon uptime, upstream client count, total requests/min, **total context budget** (~X tokens across Y tools).
   - Server table:
     ```
     NAME           STATE   TOOLS   ~TOKENS   LAST ACTIVITY
     github         ●       24      ~3,820    12s ago
     filesystem     ●        4      ~  510    2m ago
     slack          ○        0           0    —
     notion         !        0           0    errored: ECONNREFUSED
     ```
   - Footer: keybindings hint.

2. **Server detail** (enter on a row)
   - Config (redacted secrets shown as `${secret:NAME}`).
   - State machine + restart count + last error.
   - Tool list sorted by token cost.
   - Live stderr tail (bottom pane).
   - Actions: `r` restart, `t` toggle enable, `e` edit config (opens `$EDITOR`).

3. **Request log** (tab `2`)
   - Rolling view of recent MCP frames.
   - Columns: time | direction | server | method | status | duration.
   - Filters: `/` fuzzy filter, `:` method filter, `!` error-only toggle.
   - Expand a row (`enter`) → full request + response JSON in a viewport.

4. **Tools** (tab `3`)
   - All tools across all servers, sorted by token cost, descending.
   - Shows `server`, `name`, `~tokens`, `description`.
   - Purpose: "what's actually eating my budget right now?"

5. **Secrets** (tab `4`)
   - List of secret names referenced by any server, plus which servers reference each.
   - Actions: `a` add (prompt for name + value), `d` delete, `R` rotate (prompt for new value).
   - Values never displayed.

### Keybindings

Modal, vim-flavored:

| Key | Action |
|---|---|
| `1`-`4` | Switch tab |
| `j`/`k` or arrows | Navigate rows |
| `enter` | Drill into focused row |
| `esc`/`q` | Back / quit |
| `r` | Restart focused server |
| `t` | Toggle enable on focused server |
| `e` | Edit config in `$EDITOR` (then reconcile) |
| `/` | Filter |
| `?` | Help overlay |
| `R` | Force config reload |

### Theming

Default Lipgloss palette, respect `NO_COLOR`, graceful degradation on 16-color terminals. No custom themes in v0.

---

## 6. Build, Test, Distribution

### Toolchain

- **Go 1.23+.**
- **Module path:** `github.com/ayu5h-raj/mcp-gateway` (adjust if user has different GitHub org).
- **Layout:**
  ```
  mcp-gateway/
    cmd/mcp-gateway/       # main.go, Cobra-based subcommand dispatch
    internal/
      daemon/              # HTTP + socket servers, top-level daemon lifecycle
      supervisor/          # child process management + state machine
      aggregator/          # merging tools/resources/prompts, prefixing, caching
      router/              # route calls to correct child; progress/cancel plumbing
      config/              # JSONC parsing, schema validation, fsnotify watcher
      secret/              # go-keyring backend + resolver
      tokens/              # token estimator
      event/               # event bus + ring buffer + pub/sub
      tui/                 # Bubble Tea app + screens
      ipc/                 # admin RPC over UNIX socket; stdio bridge
      mcpext/              # MCP SDK adapters where the upstream SDK doesn't cover us
    schema/
      config.schema.json   # JSON Schema for config
    docs/
      superpowers/specs/   # this file
      README.md
    Makefile
    .goreleaser.yaml
    .github/workflows/
      ci.yml
      release.yml
    go.mod
    go.sum
  ```

### Dependencies (initial selection; subject to refinement)

- `github.com/modelcontextprotocol/go-sdk` — MCP protocol.
- `github.com/charmbracelet/bubbletea`, `.../lipgloss`, `.../bubbles` — TUI.
- `github.com/zalando/go-keyring` — OS keychain access.
- `github.com/fsnotify/fsnotify` — config watcher.
- `github.com/spf13/cobra` — CLI subcommand dispatch.
- `github.com/tidwall/jsonc` (or inline preprocessor) — JSONC → JSON.
- `github.com/rs/zerolog` or `log/slog` — structured logging (prefer stdlib `slog`).

### Testing strategy

- **Unit tests** for: config parsing, secret resolution, prefixing, token estimation, supervisor state machine, event bus.
- **Integration tests** spinning up a **fake downstream MCP server** (in-process) + real daemon + real upstream test client (using the Go SDK client side). Validate:
  - initialize handshake and capability union,
  - `tools/list` merged, prefixed, emits `list_changed` on reload,
  - `tools/call` routing,
  - progress notification pass-through,
  - cancellation routing,
  - hot-reload add/remove/restart,
  - crash + backoff + disable flow.
- **E2E smoke**: build the binary, run `daemon`, spawn MCP Inspector against it, tick basic tool/resource flows. Gated behind `make e2e` (not in CI initially).
- Coverage target: ≥70% line coverage on `internal/`, higher on `aggregator` + `router`.

### CI

GitHub Actions:
- `ci.yml` (on PR + push to `main`): `go vet`, `golangci-lint`, `go test ./...`, `go build ./...` on `darwin-arm64` and `linux-amd64`.
- `release.yml` (on tag `v*`): `goreleaser release --clean` → binaries + checksums + Homebrew formula update on tap repo.

### Distribution

- **GitHub Releases via goreleaser**: `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`, `windows-amd64` (Windows is unsupported at runtime but the build validates portability).
- **Homebrew tap**: `brew install ayushraj/mcp-gateway/mcp-gateway`. Tap repo auto-updated by goreleaser.
- **Install script**: `curl -fsSL https://raw.githubusercontent.com/ayushraj/mcp-gateway/main/install.sh | sh` — single-script installer.
- **`go install github.com/ayu5h-raj/mcp-gateway/cmd/mcp-gateway@latest`** for Go users.

### First-run UX

`mcp-gateway` with no subcommand:
- If no daemon running and no config file → run a one-shot wizard:
  1. Create `~/.mcp-gateway/`.
  2. Write a stub `config.jsonc` with comments explaining the shape.
  3. Print the exact JSON block to paste into Claude Desktop / Cursor / Claude Code config.
  4. Offer to start the daemon (`launchctl` plist on macOS or just `mcp-gateway daemon &`).
- Otherwise → show brief status + common next commands.

---

## 7. Risks and Open Questions

### Risks

1. **MCP Go SDK maturity.** The official SDK is under active development and may have gaps in Streamable HTTP / resource subscription handling. **Mitigation:** use SDK where stable, drop to raw JSON-RPC via `encoding/json` over the relevant transport where needed; keep this contained in `internal/mcpext`.

2. **Stdio subprocess signal / zombie handling on macOS.** Long-running supervisors of stdio children need careful SIGTERM / SIGKILL / process-group handling; easy to leak zombies. **Mitigation:** set `Setpgid`; explicit `syscall.Kill(-pgid, SIGTERM)` on teardown; tested on supervisor unit + integration.

3. **Token estimation accuracy.** `chars/4` heuristic may mislead users for non-English tool descriptions or schema-heavy tools. **Mitigation:** clearly labeled "~", make the estimator interface pluggable for a tiktoken-backed implementation later.

4. **Claude Desktop stdio config UX.** Users must spawn `mcp-gateway stdio` (the bridge), which requires the daemon to be running. If the daemon is down, stdio bridge fails and the client sees no tools. **Mitigation:** stdio bridge auto-starts the daemon if the socket is absent (race-safe via pidfile). Also: `launchctl` service so the daemon is auto-starting across reboots.

5. **Hot-reload races with CLI edits.** If the CLI writes config while fsnotify reloads are firing, partial-read races are possible. **Mitigation:** atomic rename on write; fsnotify filter for `RENAME | WRITE` finalization; brief debounce.

6. **Namespace absence may disappoint power users coming from MetaMCP.** They'll ask for multi-namespace configs. **Mitigation:** document explicitly in README — v0 is single aggregated endpoint by design; multi-endpoint is v2 territory.

### Open questions for implementation phase

- Exact schema for resource URI prefixing — "prefix the scheme" vs "prefix after the scheme" vs "add a query parameter" — test compatibility with known MCP servers that return resources.
- Whether to make `event_buffer_size` adjustable at runtime or only via config.
- Whether to ship a launchd plist (macOS service) in v0 or leave it manual.
- Whether to bundle a minimal "health check" tool exposed on the aggregated endpoint itself (like `gateway__ping`) for debuggability, or keep the aggregated surface strictly downstream-only.

---

## 8. Success criteria for v0

Merge when:
- A user can `brew install` the binary, run `mcp-gateway`, follow the wizard, paste the emitted JSON block into Claude Desktop, add 3+ MCP servers via the TUI, and see all tools show up in Claude Desktop — **without editing a JSON file by hand**.
- The TUI shows live request flow when Claude Desktop calls a tool.
- The context-budget number in the TUI updates when a server is toggled.
- Adding a secret via `mcp-gateway secret set X` makes `${secret:X}` resolvable in config, with no value ever appearing in config file, log, or TUI.
- Killing a downstream server externally results in its TUI state going `errored → restarting → running` (or `disabled` after 5 failures) without the daemon dying.
- `docker-compose`-free. No PostgreSQL. No Node. No Python. One binary.

---

## 9. References

- **Research brief** (Apr 2026 landscape survey): see conversation context.
- **MetaMCP**: https://github.com/metatool-ai/metamcp (issues #272, #264, #263, #265, #266, #277).
- **1MCP**: https://github.com/1mcp-app/agent.
- **mcp-hub**: https://github.com/ravitemer/mcp-hub.
- **MCP spec 2025-06-18**: https://modelcontextprotocol.io/specification/2025-06-18.
- **MCP Go SDK**: https://github.com/modelcontextprotocol/go-sdk.
- **Bubble Tea**: https://github.com/charmbracelet/bubbletea.
- **go-keyring**: https://github.com/zalando/go-keyring.
- **Awesome MCP Gateways**: https://github.com/e2b-dev/awesome-mcp-gateways.
- **Q1 2026 ecosystem survey**: https://www.heyitworks.tech/blog/mcp-aggregation-gateway-proxy-tools-q1-2026.
- **Tool-name collision bugs**: Claude Code [#50319](https://github.com/anthropics/claude-code/issues/50319); [Cursor forum thread](https://forum.cursor.com/t/mcp-tools-name-collision-causing-cross-service-tool-call-failures/70946).
- **Token bloat**: [The New Stack — How to Reduce MCP Token Bloat](https://thenewstack.io/how-to-reduce-mcp-token-bloat/).
