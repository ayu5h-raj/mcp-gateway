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

**Status:** v0.2.0-alpha — full mutation CLI, admin RPC over UNIX socket, env-var resolver, pidfile-protected daemon lifecycle. TUI (Bubble Tea) is the next milestone (Plan 03).

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
- TUI manager (Bubble Tea) — k9s-style live ops view (Plan 03)
- Context-budget meter — surfaces the #1 unsolved MCP pain ("tool defs ate 72% of my context") (TUI surface)
- Single-binary distribution via Homebrew tap + curl-pipe-sh installer (Plan 04)

---

## Install

### From source (works today)

```bash
git clone https://github.com/ayu5h-raj/mcp-gateway
cd mcp-gateway
make build
./bin/mcp-gateway --help
```

### Pre-built (coming in v1.0 — Plan 04)

```bash
brew install ayu5h-raj/mcp-gateway/mcp-gateway
# or
curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/install.sh | sh
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

## Run

```bash
mcp-gateway daemon
# INFO mcp-gateway listening addr=127.0.0.1:7823
# INFO attached server=github prefix=github
# INFO attached server=filesystem prefix=filesystem
```

Tools end up exposed as `<server>__<tool>` — e.g. `github__create_issue`, `filesystem__read_file`.

---

## Connect a client

### Claude Desktop (`~/Library/Application Support/Claude/claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "gateway": {
      "command": "/usr/local/bin/mcp-gateway",
      "args": ["stdio", "--port", "7823"]
    }
  }
}
```

`mcp-gateway stdio` is a thin bridge: stdio in → HTTP POST to the daemon → response back on stdout. One bridge process per Claude session, but only one daemon.

### Cursor / Claude Code / VS Code / Zed (HTTP-capable)

Point them directly at the daemon's Streamable HTTP endpoint:

```json
{ "mcpServers": { "gateway": { "url": "http://127.0.0.1:7823/mcp" } } }
```

---

## Day-to-day commands

```bash
mcp-gateway start                       # spawn the daemon (detached)
mcp-gateway status                      # status from /admin/status
mcp-gateway list                        # all servers + state + token cost

# Add a server (prefix defaults to name). Two patterns for credentials:

# 1) Hardcoded — fastest, fine for a local-only config:
mcp-gateway add github \
  --command npx --arg -y --arg @modelcontextprotocol/server-github \
  --env GITHUB_TOKEN=ghp_xxx

# 2) Pull from your shell env at spawn time:
mcp-gateway add github \
  --command npx --arg -y --arg @modelcontextprotocol/server-github \
  --env GITHUB_TOKEN='${env:GITHUB_TOKEN}'

mcp-gateway disable github              # stop the child but keep config
mcp-gateway enable github               # start it again
mcp-gateway rm github                   # remove from config

mcp-gateway secret list                 # which env vars does the config want? are they set?

mcp-gateway stop                        # SIGTERM via pidfile
mcp-gateway restart                     # stop + start
```

You can still hand-edit `~/.mcp-gateway/config.jsonc`; the daemon hot-reloads. The CLI just removes the need.

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

## What works in v0.2

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
- ✅ **Token-cost estimator** (chars/4 heuristic; surfaced in `mcp-gateway list`)
- ✅ macOS + Linux

## What's deferred (Plan 03 / 04 / later)

- **Plan 03:** TUI (Bubble Tea, k9s-style) — 5 tabs; subscribes to `/admin/events`
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
