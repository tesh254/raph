#!/bin/sh
set -eu

REPO=${RAPH_REPO:-tesh254/raph}
VERSION=${RAPH_VERSION:-latest}
BIN_DIR=${BIN_DIR:-}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

if [ -z "$BIN_DIR" ]; then
  if [ -w /usr/local/bin ]; then
    BIN_DIR=/usr/local/bin
  else
    BIN_DIR=$HOME/.local/bin
  fi
fi

mkdir -p "$BIN_DIR"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
  darwin) OS=darwin ;;
  linux) OS=linux ;;
  *)
    echo "Unsupported operating system: $OS" >&2
    echo "Use the Windows release asset manually on Windows." >&2
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

ASSET="raph_${OS}_${ARCH}.tar.gz"
TMP_DIR=$(mktemp -d)
ARCHIVE="$TMP_DIR/$ASSET"
CHECKSUMS="$TMP_DIR/checksums.txt"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/$REPO/releases/latest/download/$ASSET"
else
  case "$VERSION" in
    v*) TAG=$VERSION ;;
    *) TAG="v$VERSION" ;;
  esac
  URL="https://github.com/$REPO/releases/download/$TAG/$ASSET"
fi

echo "Downloading $URL"
if need_cmd curl; then
  curl -fsSL "$URL" -o "$ARCHIVE"
  curl -fsSL "$(dirname "$URL")/checksums.txt" -o "$CHECKSUMS"
elif need_cmd wget; then
  wget -qO "$ARCHIVE" "$URL"
  wget -qO "$CHECKSUMS" "$(dirname "$URL")/checksums.txt"
else
  echo "curl or wget is required" >&2
  exit 1
fi

EXPECTED=$(awk -v asset="$ASSET" '$2 == asset || $2 == "*" asset { print $1; exit }' "$CHECKSUMS")
if [ -z "$EXPECTED" ]; then
  echo "Checksum for $ASSET not found" >&2
  exit 1
fi
if need_cmd sha256sum; then
  ACTUAL=$(sha256sum "$ARCHIVE" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Checksum mismatch for $ASSET" >&2
  exit 1
fi

tar -xzf "$ARCHIVE" -C "$TMP_DIR"
BINARY_PATH=$(find "$TMP_DIR" -type f -name raph | head -n 1)
if [ -z "$BINARY_PATH" ]; then
  echo "Could not locate extracted raph binary" >&2
  exit 1
fi
install "$BINARY_PATH" "$BIN_DIR/raph"

echo "Installed raph to $BIN_DIR/raph"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Add $BIN_DIR to your PATH to run 'raph'." ;;
esac
