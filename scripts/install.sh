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
