#!/bin/sh
# Installs relevo-host from the latest GitHub release.
#   curl -fsSL https://raw.githubusercontent.com/azratul/relevo/main/install.sh | sh
# Options (environment variables):
#   RELEVO_VERSION=v0.1.0          a specific release instead of the latest
#   RELEVO_INSTALL_DIR=/usr/local/bin   where to put the binary (default ~/.local/bin)
set -eu

REPO="${RELEVO_REPO:-azratul/relevo}"
VERSION="${RELEVO_VERSION:-latest}"
BIN_DIR="${RELEVO_INSTALL_DIR:-$HOME/.local/bin}"
BASE="${RELEVO_DOWNLOAD_BASE:-}"

fail() { echo "relevo install: $*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin|freebsd) ;;
  *) fail "unsupported system: $os (on Windows use install.ps1)" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac
[ "$os" = freebsd ] && [ "$arch" != amd64 ] && fail "only freebsd/amd64 builds are published"

if [ -z "$BASE" ]; then
  if [ "$VERSION" = latest ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
  else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
  fi
fi
asset="relevo-host_${os}_${arch}.tar.gz"

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else fail "curl or wget is required"; fi
}
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  elif command -v sha256 >/dev/null 2>&1; then sha256 -q "$1"
  else fail "no SHA-256 tool found"; fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $asset ($VERSION)…"
fetch "$BASE/$asset" "$tmp/$asset" || fail "download failed: $BASE/$asset"
fetch "$BASE/checksums.txt" "$tmp/checksums.txt" || fail "download failed: $BASE/checksums.txt"
want=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || fail "$asset is not listed in checksums.txt"
[ "$(sha256 "$tmp/$asset")" = "$want" ] || fail "checksum mismatch for $asset"

tar -xzf "$tmp/$asset" -C "$tmp" relevo-host
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/relevo-host" "$BIN_DIR/relevo-host" 2>/dev/null || { cp "$tmp/relevo-host" "$BIN_DIR/relevo-host"; chmod 0755 "$BIN_DIR/relevo-host"; }
echo "Installed $("$BIN_DIR/relevo-host" version) to $BIN_DIR/relevo-host"
case ":$PATH:" in
  *":$BIN_DIR:"*) echo "Run: relevo-host" ;;
  *) echo "Add $BIN_DIR to your PATH, or run: $BIN_DIR/relevo-host" ;;
esac
