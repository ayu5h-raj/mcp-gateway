#!/usr/bin/env sh
# install.sh — download and install mcp-gateway from GitHub Releases
# Usage: curl -fsSL https://raw.githubusercontent.com/ayu5h-raj/mcp-gateway/main/scripts/install.sh | sh
set -e

REPO="ayu5h-raj/mcp-gateway"
BINARY="mcp-gateway"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Darwin) OS_NAME="macos" ;;
  Linux)  OS_NAME="linux" ;;
  *)
    echo "Unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH_NAME="intel" ;;
  arm64|aarch64) ARCH_NAME="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# Resolve the latest release tag
if command -v curl >/dev/null 2>&1; then
  FETCH="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  FETCH="wget -qO-"
else
  echo "curl or wget is required" >&2
  exit 1
fi

LATEST_TAG=$($FETCH "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\(.*\)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
  echo "Could not determine latest release tag" >&2
  exit 1
fi

VERSION="${LATEST_TAG#v}"
TARBALL="mcp-gateway-${OS_NAME}-${ARCH_NAME}-${LATEST_TAG}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${TARBALL}"

echo "Installing mcp-gateway ${LATEST_TAG} (${OS_NAME}/${ARCH_NAME})..."

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

$FETCH "$URL" -o "$TMP_DIR/$TARBALL" 2>/dev/null || $FETCH "$URL" > "$TMP_DIR/$TARBALL"
tar -xzf "$TMP_DIR/$TARBALL" -C "$TMP_DIR"

# Install binaries
for bin in mcp-gateway mgw-smoke; do
  if [ -f "$TMP_DIR/$bin" ]; then
    if [ -w "$INSTALL_DIR" ]; then
      cp "$TMP_DIR/$bin" "$INSTALL_DIR/$bin"
      chmod +x "$INSTALL_DIR/$bin"
    else
      sudo cp "$TMP_DIR/$bin" "$INSTALL_DIR/$bin"
      sudo chmod +x "$INSTALL_DIR/$bin"
    fi
    echo "  installed $INSTALL_DIR/$bin"
  fi
done

echo "Done! Run 'mcp-gateway --version' to verify."
