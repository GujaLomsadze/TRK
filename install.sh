#!/bin/sh
# trk installer: curl -fsSL https://raw.githubusercontent.com/GujaLomsadze/trk/main/install.sh | sh
set -eu
REPO="GujaLomsadze/trk"
DIR="${TRK_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "trk: unsupported OS '$os' (on Windows use Scoop or the .zip from GitHub Releases)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "trk: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

asset="trk_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/latest/download"
[ -n "${TRK_VERSION:-}" ] && base="https://github.com/$REPO/releases/download/$TRK_VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $asset"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
want=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "trk: checksum mismatch for $asset" >&2
  exit 1
fi
tar -xzf "$tmp/$asset" -C "$tmp" trk
mkdir -p "$DIR"
install -m 0755 "$tmp/trk" "$DIR/trk"
echo "trk installed to $DIR/trk"
case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "note: $DIR is not on your PATH; add it to your shell profile" ;;
esac
echo "next: trk init"
