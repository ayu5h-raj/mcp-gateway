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
