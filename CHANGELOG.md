# Changelog

All notable changes to mcp-gateway are documented here. Versions follow [SemVer](https://semver.org/).

## [Unreleased]

(Nothing here yet.)

## [1.0.2] — 2026-04-25

### Fixed
- `mcp-gateway service install` now snapshots the user's actual login-shell PATH into the launchd plist instead of hardcoding `/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin`. Previously, child MCP servers spawned by the launchd-launched daemon could not find binaries installed via nvm, asdf, mise, or pyenv (e.g. `npx` from a node version manager). Re-run `mcp-gateway service uninstall && mcp-gateway service install` after upgrading to pick up the fix on an existing install.

## [1.0.1] — 2026-04-25

### Fixed
- `mcp-gateway init` no longer aborts when an MCP client config contains an HTTP-transport server (e.g. `"type": "http"` entries with no `command`). Such entries are now flagged in the discovery list as `(HTTP transport — not yet supported, leave in <client>)`, the per-client import count reflects only stdio entries, and the patch step preserves the HTTP entries in the client's own config so they keep working natively. Native HTTP/SSE downstream support remains deferred to a later release.

## [1.0.0] — 2026-04-25

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
