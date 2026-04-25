# mcp-gateway

> Local-first MCP aggregator. One config, one binary, every MCP server.

`mcp-gateway` runs as a single Go binary on your machine. You configure all your [Model Context Protocol](https://modelcontextprotocol.io) servers in **one** place; every MCP-capable app (Claude Desktop, Cursor, Claude Code, VS Code, Zed, Windsurf, …) just points at the gateway and gets all of them.

```
 Claude Desktop ──┐
 Cursor ──────────┤──► mcp-gateway ──► github
 Claude Code ─────┤      (one binary)   filesystem
 VS Code ─────────┘                     kite
                                        slack
                                        ...
```

**Status:** v0.3.0-alpha — adds a k9s-style TUI (`mcp-gateway tui`) on top of the v0.2 substrate. Live observability + control from the terminal.

---

## Why another MCP gateway?

Several aggregators exist (MetaMCP, 1MCP, mcp-hub, mcp-proxy). This one is positioned differently:

| | mcp-gateway | Most others |
|---|---|---|
| Distribution | single static Go binary, `brew` / `curl \| sh` | Docker / Node / Python required |
| Config | one JSONC file, hot-reloaded | web UI, database, often both |
| Tool name strategy | `<server>__<tool>` (matches Claude convention) | varies |
| Footprint | < 15 MB binary, runs on a phone | Docker Compose, Postgres, 2-4 GB RAM |

Roadmap features that double down on this positioning:
- TUI manager (Bubble Tea) — k9s-style live ops view ✅ **shipped in v0.3**
- Context-budget meter — surfaces the #1 unsolved MCP pain ("tool defs ate 72% of my context") ✅ **Tools tab**
- Single-binary distribution via Homebrew tap + curl-pipe-sh installer (Plan 04)

---

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

---

## Configure

Create `~/.mcp-gateway/config.jsonc`:

```jsonc
{
  "version": 1,

  "daemon": {
    "http_port": 7823,
    "log_level": "info"
  },

  // Same shape as Claude Desktop's mcpServers — paste blocks in directly.
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "ghp_..." },
      "enabled": true
    },
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/Documents"],
      "enabled": true
    }
  }
}
```

The file is **hot-reloaded** via `fsnotify` — edit + save and the daemon reconciles without a restart.

---

## TUI

`mcp-gateway tui` opens a k9s-style live view of the running daemon:

- **Servers tab** — all configured servers, their state, tool count, token cost. `j`/`k` to navigate, `enter` to drill in, `r` to restart, `t` to toggle enable/disable, `esc` to go back.
- **Events tab** — streaming MCP request/response events + lifecycle events (`child.attached`, `child.crashed`, `tools.changed`, …) via live SSE from `/admin/events`.
- **Tools tab** — all tools across all servers, sorted by estimated token cost. Answers "what's eating my context?"

Press `?` for the key map, `q` to quit. Requires the daemon to be running; if it's not, the TUI shows `daemon disconnected` in the header and auto-reconnects when `mcp-gateway start` brings it back.

![terminal-ui] *(screenshot coming)*

---

## Verify

A small smoke client ships in this repo. It drives the gateway through the full MCP lifecycle (`initialize` → `notifications/initialized` → `tools/list` → `ping` → optional `tools/call`) and reports PASS/FAIL per step:

```bash
go build -o bin/mgw-smoke ./cmd/mgw-smoke
./bin/mgw-smoke --port 7823
# PASS initialize       server=mcp-gateway/0.1
# PASS notifications/initialized   no reply (correct)
# PASS tools/list       22 tools: kite__cancel_order, …
# PASS resources/list   0 resources
# PASS prompts/list     0 prompts
# PASS ping             ack
# PASS notifications/cancelled   no reply (correct)
# SMOKE PASSED
```

Invoke a real tool:

```bash
./bin/mgw-smoke --port 7823 --call kite__get_holdings --args '{}'
```

---

## Observe

```bash
# daemon's own log
tail -f /tmp/mgw-daemon.log

# per-child stderr (everything the downstream MCP server prints)
ls   ~/.mcp-gateway/servers/
tail -f ~/.mcp-gateway/servers/github.log

# quick health check
mcp-gateway status
```

The TUI (Bubble Tea, k9s-style) lands in **Plan 03** — see [the design spec](docs/superpowers/specs/2026-04-23-mcp-gateway-design.md) and [Plan 02](docs/superpowers/plans/2026-04-24-mcp-gateway-plan-02-substrate.md) for what shipped in v0.2.

---

## What works in v0.3

- ✅ Aggregate N stdio MCP servers behind one endpoint
- ✅ Streamable HTTP `POST /mcp` for HTTP-capable clients
- ✅ Stdio bridge for Claude Desktop and other stdio-only clients
- ✅ Tools, resources, prompts — all merged with `<server>__<tool>` prefixing
- ✅ Hot reload on config change (fsnotify)
- ✅ Process supervisor with exponential backoff, process-group isolation, per-child stderr capture
- ✅ Graceful child-exit handling — inflight requests get JSON-RPC errors, not parked goroutines
- ✅ Concurrent-safe MCP client (write mutex, callback mutex)
- ✅ **Pidfile-protected daemon** — `mcp-gateway start` / `stop` / `restart` / `status`
- ✅ **Mutation CLI** — `add` / `rm` / `enable` / `disable` / `list`
- ✅ **Admin RPC** over UNIX socket (`/admin/{status,servers,tools,events,secret,config}`); SSE on `/admin/events`
- ✅ **`${env:NAME}` resolver** in config env values (and `secret list` to see what's referenced + whether each is set)
- ✅ **In-process event bus** + ring buffer (substrate for the TUI)
- ✅ **Token-cost estimator** (chars/4 heuristic; surfaced in `mcp-gateway list` and the TUI Tools tab)
- ✅ **k9s-style TUI** — `mcp-gateway tui` with Servers / Events / Tools tabs, live via SSE + 2s polling
- ✅ macOS + Linux

## What's deferred (Plan 04 / later)

- **Plan 04:** First-run wizard, launchd plist, goreleaser, Homebrew tap, install.sh
- **Later:** macOS Keychain (and Linux/Windows) secret backends — the parser is scheme-aware so adding `${keychain:NAME}` is non-breaking
- **Later:** HTTP / SSE downstream MCP servers (currently stdio only)
- **Later:** OAuth passthrough for remote MCPs (GitHub, Slack, Notion)
- **Later:** Sampling and elicitation forwarding (server → client requests)
- **Later:** Per-client tool scoping
- **Later:** Windows

---

## Architecture

Two processes, one binary:

- **`mcp-gateway daemon`** — long-running. Reads config, supervises stdio MCP children, exposes one merged endpoint via Streamable HTTP on `127.0.0.1:7823/mcp`.
- **`mcp-gateway stdio`** — a thin newline-framed bridge. Spawned by stdio-only clients (Claude Desktop). Stateless.

Internal layout:

```
cmd/
  mcp-gateway/        # Cobra dispatcher (daemon, stdio, status)
  mgw-smoke/          # standalone MCP client for verification
internal/
  config/             # JSONC parser + validator + fsnotify watcher
  supervisor/         # process state machine + exponential backoff + pgid signaling
  mcpchild/           # JSON-RPC MCP client over stdio pipes
  aggregator/         # merges + prefixes + routes tools/resources/prompts
  daemon/             # wires everything; serves POST /mcp
  bridge/             # stdio ↔ HTTP proxy
```

Full architecture, design rationale, and roadmap: [`docs/superpowers/specs/`](docs/superpowers/specs/) and [`docs/superpowers/plans/`](docs/superpowers/plans/).

---

## Develop

```bash
make build      # bin/mcp-gateway
make test       # unit + integration, race-detector enabled
make e2e        # builds binary, spawns child, drives full lifecycle
make lint       # golangci-lint v1.64.8 (CI-pinned)
make vet        # go vet
```

CI runs lint + tests on `ubuntu-latest` and `macos-latest`. See `.github/workflows/ci.yml`.

The repo ships with [`docs/superpowers/`](docs/superpowers/) — the original design spec and step-by-step implementation plan that produced this code (under TDD with two-stage subagent code review). Useful as both documentation and a reference for how to do agent-assisted development on a non-trivial Go project.

---

## License

MIT. See [LICENSE](LICENSE).
