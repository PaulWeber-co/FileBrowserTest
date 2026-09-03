#!/usr/bin/env bash
# Baut SpeedNAS fuer Windows, Linux und macOS nach dist/.
#
# Aufruf:  ./scripts/build.sh [version]
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"

rm -rf dist
mkdir -p dist

build() {
  local os="$1" arch="$2" ext="$3" label="$4"
  local out="dist/speednas-${os}-${arch}${ext}"
  echo "  ${label}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/speednas
}

echo "SpeedNAS ${VERSION} wird gebaut:"
build windows amd64 .exe "Windows (64 Bit)"
build windows arm64 .exe "Windows (ARM)"
build linux   amd64 ""   "Linux (64 Bit)"
build linux   arm64 ""   "Linux (ARM, z. B. Raspberry Pi 4/5)"
build linux   arm   ""   "Linux (ARM 32 Bit, aeltere Raspberry Pi)"
build darwin  amd64 ""   "macOS (Intel)"
build darwin  arm64 ""   "macOS (Apple Silicon)"

echo
ls -lh dist/ | awk 'NR>1 {printf "  %-34s %s\n", $9, $5}'
echo
echo "Fertig. Fuer Windows genuegt dist/speednas-windows-amd64.exe -"
echo "eine einzelne Datei, die Oberflaeche steckt mit drin."
