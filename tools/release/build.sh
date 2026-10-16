#!/usr/bin/env bash
# Builds release archives of relevo-host into dist/release/.
# Usage: tools/release/build.sh VERSION OS/ARCH [OS/ARCH...]
#   e.g. tools/release/build.sh v0.1.0 linux/amd64 windows/amd64
# Archive names carry no version, so ".../releases/latest/download/<name>"
# is a stable URL for install.sh. CGO_ENABLED is taken from the environment
# (0 by default; the macOS job sets 1 so the binary includes the tray).
set -euo pipefail
cd "$(dirname "$0")/../.."
VERSION=${1:?version}; shift
OUT=dist/release
mkdir -p "$OUT"
export CGO_ENABLED=${CGO_ENABLED:-0}
for target in "$@"; do
  os=${target%/*}; arch=${target#*/}
  name=relevo-host_${os}_${arch}
  work=$(mktemp -d)
  exe=relevo-host; [ "$os" = windows ] && exe=relevo-host.exe
  GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$work/$exe" ./cmd/relevo-host
  cp LICENSE README.md "$work/"
  if [ "$os" = windows ]; then
    (cd "$work" && python3 -c 'import sys,zipfile; z=zipfile.ZipFile(sys.argv[1],"w",zipfile.ZIP_DEFLATED); [z.write(f) for f in sys.argv[2:]]' "$OLDPWD/$OUT/$name.zip" "$exe" LICENSE README.md)
  else
    tar -C "$work" -czf "$OUT/$name.tar.gz" "$exe" LICENSE README.md
  fi
  rm -rf "$work"
  echo "built $name"
done
