# CLAUDE.md — mcp-gateway

This file is read automatically by Claude Code when working in this repo.

## What this project is

`mcp-gateway` is a local-first, single-binary Go daemon that aggregates N stdio
MCP (Model Context Protocol) servers behind one Streamable-HTTP endpoint plus a
stdio bridge. v0.1.0-alpha shipped 2026-04-24 and is in active use. Positioning
is "k9s for MCP" — terminal-native, zero runtime deps, distinct from MetaMCP /
1MCP / mcp-hub which need Docker/Node/Postgres.

Detailed design: `docs/superpowers/specs/2026-04-23-mcp-gateway-design.md`.
Detailed v0.1 implementation plan: `docs/superpowers/plans/2026-04-23-mcp-gateway-plan-01-foundation.md`.

## Layout

```
cmd/mcp-gateway/   # Cobra: daemon | stdio | status
cmd/mgw-smoke/     # standalone MCP smoke client
internal/
  config/          # JSONC config + fsnotify watcher (the only config layer)
  supervisor/      # spawn N children, state machine, backoff, pgid signaling
  mcpchild/        # JSON-RPC MCP client (downstream-facing) — hand-rolled, no SDK
  aggregator/      # merge + prefix + route tools/resources/prompts
  daemon/          # wires it all together; serves POST /mcp
  bridge/          # stdio ↔ HTTP proxy for stdio-only clients (Claude Desktop)
  testutil/fakechild/  # in-memory MCP server used in tests
```

## Conventions

- **TDD.** Every package was built test-first. Match the pattern when adding code:
  write the failing test → run it to confirm it fails → implement minimally →
  re-run → commit. Tests under `_test.go` are excluded from `errcheck` and
  `gocritic` in `.golangci.yml` so don't fight the linter there.
- **Tool name prefixing.** `<server>__<tool>` (double underscore). `prefix.go`
  is the canonical implementation; do not reinvent prefixing elsewhere.
- **Notifications.** Per JSON-RPC 2.0, notifications (no id, or method in the
  `notifications/*` namespace) MUST NOT receive a response. The daemon returns
  HTTP 202 with no body for notifications; the bridge skips empty bodies. See
  `daemon/http.go:isNotification` and `bridge/bridge.go`.
- **Concurrency model.** mcpchild uses a *separate* `writeMu` (not the same as
  `mu`) to serialize stdio writes. Callbacks are protected by `mu` and snapshotted
  before invocation. Inflight requests are drained with an error response when
  the child exits. Don't break these invariants — they exist because review
  caught real races (see commit `b5453d7`).
- **Aggregator error handling.** `RefreshTools` fails fast on any child error;
  `RefreshResources` and `RefreshPrompts` only swallow JSON-RPC `-32601`
  ("method not found"), propagating real errors. See `isMethodNotFound` in
  `aggregator/aggregator.go`.
- **Supervisor restart.** Backoff is gated by `nextStartAt` per server so an
  unrelated `wake()` from a different server doesn't bypass the wait. After a
  child runs stably for >30s, restart counter resets. Don't change the
  restart-respecting logic without re-running `TestSupervisor_BackoffHonoredAcrossUnrelatedWakes`.

## Commands

```bash
make build      # → bin/mcp-gateway (and bin/mgw-smoke if you `go build ./cmd/mgw-smoke`)
make test       # go test -race -count=1 ./...
make e2e        # builds real binaries, drives full lifecycle (build tag: e2e)
make lint       # golangci-lint v1.64.8 (must match CI)
make vet        # go vet
make fmt        # gofmt -s -w .
```

After any non-trivial change, the canonical "is this still working" check:

```bash
make test && make e2e && go vet ./...
```

For end-user wire-protocol verification (catches Claude Desktop–style
validation issues):

```bash
./bin/mcp-gateway daemon &      # in one shell
./bin/mgw-smoke --port 7823     # in another — should print SMOKE PASSED
```

## Style

- `errors.New` for plain-string errors; `fmt.Errorf` only when there's a `%w`
  or `%s` — `revive` and `staticcheck` flag the bare-string `Errorf`.
- Use `_` for unused Cobra handler params (`func(_ *cobra.Command, _ []string)`).
- Use `_ = ...Close()` for genuinely-intentional discards. Don't suppress
  errcheck blindly.
- Prefer the standard library's `log/slog` over any third-party logger.
- `MCPServers` (initialism upper case) — not `McpServers`. Same rule
  everywhere (URL, HTTP, JSON, …).
- File responsibilities are kept narrow. If a file grows past ~300 lines,
  consider splitting before adding more.

## What's NOT in v0.1 (don't be surprised these are missing)

The following are explicitly deferred to v0.2 and beyond. If asked to add one
of these, sanity-check against the plan first — there are intentional design
decisions on order:

- TUI (`mcp-gateway tui`) — Bubble Tea, k9s-style
- Event bus + ring buffer (TUI's data source)
- Token estimator (chars/4 heuristic, surfaced in TUI)
- `${secret:NAME}` resolver — go-keyring backend
- Admin RPC over UNIX socket — what TUI and CLI mutation commands talk to
- `mcp-gateway add | rm | enable | disable | secret | start | stop` subcommands
- First-run wizard, launchd plist
- Goreleaser, brew tap, install.sh
- HTTP / SSE downstream MCP servers (currently stdio only)
- OAuth passthrough for remote MCPs
- Sampling and elicitation forwarding (server → client requests)
- Per-client tool scoping
- Windows support

## Working with the historical commits

Every commit is small and atomic. The branch went through:

1. Design spec + plan (`docs/superpowers/`) — *read these before substantial changes*.
2. Phase 0–7: TDD implementation per the plan, with two-stage code review
   (spec compliance + code quality) per phase. Many `fix:` commits are review
   findings — they're intentional and well-documented.
3. Bridge / daemon notification fix (commit `5df4cc1`) — caught by Claude
   Desktop's validator. Critical correctness fix.

Don't rebase or amend old commits. Create a new commit for any fix.
