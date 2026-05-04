#!/usr/bin/env sh
# Polaris CLI installer.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/berkaycubuk/polaris-agent/main/scripts/install.sh | sh
#
# Environment variables:
#   POLARIS_VERSION   Pin a specific release (e.g. v0.2.0). Defaults to latest.
#   POLARIS_INSTALL   Install directory. Defaults to /usr/local/bin if writable
#                     (or via sudo), otherwise $HOME/.local/bin.

set -eu

REPO="berkaycubuk/polaris-agent"
BINARY="polaris"

log()  { printf '\033[0;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[0;33m==>\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[0;31m==>\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || err "missing required tool: $1"; }

need uname
need tar
need mktemp

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  fetch_to() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  fetch_to() { wget -qO "$2" "$1"; }
else
  err "need either curl or wget"
fi

uname_os() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    linux)  echo linux ;;
    darwin) echo darwin ;;
    *) err "unsupported OS: $os" ;;
  esac
}

uname_arch() {
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) echo amd64 ;;
    arm64|aarch64) echo arm64 ;;
    *) err "unsupported architecture: $arch" ;;
  esac
}

OS=$(uname_os)
ARCH=$(uname_arch)

VERSION="${POLARIS_VERSION:-}"
if [ -z "$VERSION" ]; then
  log "resolving latest release"
  VERSION=$(fetch "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n1)
  [ -n "$VERSION" ] || err "could not determine latest version (set POLARIS_VERSION to override)"
fi

# goreleaser strips the leading v from {{.Version}} in archive names
NUM_VERSION="${VERSION#v}"
ARCHIVE="${BINARY}_${NUM_VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$ARCHIVE"

pick_install_dir() {
  if [ -n "${POLARIS_INSTALL:-}" ]; then
    echo "$POLARIS_INSTALL"
    return
  fi
  if [ -w /usr/local/bin ] 2>/dev/null; then
    echo /usr/local/bin
  elif command -v sudo >/dev/null 2>&1 && [ -d /usr/local/bin ]; then
    echo /usr/local/bin
  else
    echo "$HOME/.local/bin"
  fi
}

INSTALL_DIR=$(pick_install_dir)
mkdir -p "$INSTALL_DIR" 2>/dev/null || true

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

log "downloading $URL"
fetch_to "$URL" "$TMP/$ARCHIVE" || err "download failed: $URL"

log "extracting"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

[ -f "$TMP/$BINARY" ] || err "binary '$BINARY' not found in archive"
chmod +x "$TMP/$BINARY"

DEST="$INSTALL_DIR/$BINARY"
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/$BINARY" "$DEST"
else
  log "installing to $DEST (requires sudo)"
  sudo mv "$TMP/$BINARY" "$DEST"
fi

log "installed $BINARY $VERSION to $DEST"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) warn "$INSTALL_DIR is not in your PATH. Add it with:"
     warn "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.profile" ;;
esac

log "run 'polaris setup' to get started"
