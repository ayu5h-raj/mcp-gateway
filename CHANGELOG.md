# Changelog

All notable changes to mcp-gateway are documented here. Versions follow [SemVer](https://semver.org/).

## [Unreleased]

(Nothing here yet.)

## [1.0.6] — 2026-04-26

### Fixed
- `mcp-gateway stop` against a launchd-managed daemon now refuses up front instead of pretending to time out. Pre-1.0.6, `stop` would SIGTERM the daemon, launchd would respawn it within milliseconds, and the post-kill check (which polled for the socket file's absence) would falsely report `daemon did not exit within 5s`. v1.0.6 detects launchd ownership via `launchctl print` matching the running pid and prints the correct command instead: `mcp-gateway service uninstall` (to remove auto-start) or `launchctl kickstart -k gui/$(id -u)/com.ayu5h-raj.mcp-gateway` (to restart in place). `mcp-gateway restart` carries the same guard.
- `mcp-gateway stop` now polls the daemon's pid for death (`kill -0`) instead of polling for the socket file's absence. Socket-polling was a false positive against any auto-restart supervisor (launchd, systemd, even an out-of-band `mcp-gateway start &` in a wrapper script). The post-kill grace window grew from 5s → 10s to give the daemon room to drain its MCP children's stdio buffers (notably `npx mcp-remote` wrappers, which can take a few seconds to exit).

## [1.0.5] — 2026-04-25

### Fixed
- `mcp-gateway service install` and `mcp-gateway init` no longer record the brew Cellar path (e.g. `/opt/homebrew/Cellar/mcp-gateway/1.0.4/bin/mcp-gateway`) into the launchd plist or patched client configs. The Cellar path is removed by `brew upgrade`, so previously, every brew bump silently broke auto-start and the stdio bridge until you re-ran `init`. v1.0.5 records the stable symlink (`/opt/homebrew/bin/mcp-gateway`) instead. **Brew upgraders: re-run `mcp-gateway service uninstall && mcp-gateway service install` once after upgrading to pick up the fix on the existing plist; client configs auto-heal next time you run `mcp-gateway init -y --force`.**

### Changed
- The TUI now flips a server's row state to `stopping`/`starting`/`restarting` immediately when you press `t` (toggle) or `r` (restart), instead of waiting up to ~2s for the next admin poll to reflect the change. The real daemon-side state overwrites within one poll cycle. New `◒` glyph for the optimistic `stopping` state.

## [1.0.4] — 2026-04-25

### Fixed
- v1.0.3's Homebrew formula was unusable — it included an `uninstall_preflight` block, which is a Cask-only Homebrew method (Formulae don't have it). `brew install ayu5h-raj/tap/mcp-gateway` errored with `undefined method 'uninstall_preflight'`. v1.0.4 drops the broken block and instead spells out the correct uninstall order in the `caveats` block printed after `brew install`.

### Notes
- A real auto-uninstall hook for Formulae would require either repackaging as a Homebrew Cask or shipping a top-level `mcp-gateway uninstall` subcommand. Both are larger changes; deferring.

## [1.0.3] — 2026-04-25

### Fixed
- `mcp-gateway init` no longer imports an entry whose `command` resolves to the gateway binary itself. Re-running `init` against a previously-patched client config used to surface the `mcp-gateway` entry as importable, which produced a recursive supervisor → gateway → supervisor chain (visible in the TUI as `servers=1, tools=0` forever). Self-pointing entries are now flagged `(self-pointing — already an mcp-gateway entry from a prior install, skipping)`.
- `mcp-gateway service install` now waits 250ms after `launchctl bootout` and retries `launchctl bootstrap` up to 3 times when the post-bootout race produces `Bootstrap failed: 5: Input/output error` or "service is already loaded". The bootout/bootstrap pair is now reliably idempotent.
- `mcp-gateway init`'s service-install step is no longer fatal: if `launchctl bootstrap` fails after the retries, the wizard prints a clear workaround (`mcp-gateway start` to run the daemon now, `mcp-gateway service install` to retry auto-start later) and the config + client-patch work performed earlier in the wizard is preserved.
- The launchctl-bootstrap error message no longer suggests `Try re-running as root` (launchctl's own misleading default for per-user gui plists). Replaced with: `this is a per-user gui/<uid> plist; sudo will not help.`

### Changed
- Homebrew formula now includes a `caveats` block (printed after `brew install`) summarizing `mcp-gateway init` and the launchd-service lifecycle, plus an `uninstall_preflight` hook that automatically runs `mcp-gateway service uninstall` so `brew uninstall` cleanly tears down the launchd plist. Existing data at `~/.mcp-gateway/` is intentionally left untouched on uninstall (config + logs); the caveats text says so explicitly.

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
