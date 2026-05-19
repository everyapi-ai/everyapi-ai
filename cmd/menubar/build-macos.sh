#!/usr/bin/env bash
# build-macos.sh — assemble EveryAPI.app from the everyapi-menubar binary.
#
# Usage:
#   ./build-macos.sh            # builds for the host arch into ./dist
#   ARCH=arm64 ./build-macos.sh # explicit arch (arm64 | amd64)
#   ARCH=universal ./build-macos.sh
#                               # builds both, lipo'd into one binary
#
# Output: dist/EveryAPI.app — open it with `open dist/EveryAPI.app`.
# Code signing / notarization is deferred (see GOAL.md M5).

set -euo pipefail

cd "$(dirname "$0")"
SCRIPT_DIR=$(pwd)
CLI_DIR=$(cd ../.. && pwd)
ARCH=${ARCH:-$(uname -m)}
case "$ARCH" in
  arm64|aarch64) GOARCH=arm64 ;;
  x86_64|amd64)  GOARCH=amd64 ;;
  universal)     GOARCH=universal ;;
  *) echo "unsupported ARCH=$ARCH" >&2; exit 1 ;;
esac

VERSION=${VERSION:-$(cd "$CLI_DIR" && git describe --tags --abbrev=0 2>/dev/null || echo "0.0.0-dev")}
VERSION=${VERSION#v}
COMMIT=${COMMIT:-$(cd "$CLI_DIR" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")}
LDFLAGS="-s -w -X github.com/everyapi-ai/everyapi-ai/internal/version.Version=$VERSION -X github.com/everyapi-ai/everyapi-ai/internal/version.Commit=$COMMIT"

DIST="$SCRIPT_DIR/dist"
APP="$DIST/EveryAPI.app"
rm -rf "$DIST"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

build_one() {
  local goarch="$1" out="$2"
  echo "→ building everyapi-menubar ($goarch) → $out"
  (cd "$CLI_DIR" && \
    CGO_ENABLED=1 GOOS=darwin GOARCH="$goarch" \
    go build -o "$out" -ldflags="$LDFLAGS" ./cmd/menubar)
}

BIN="$APP/Contents/MacOS/everyapi-menubar"
if [ "$GOARCH" = "universal" ]; then
  build_one arm64 "$BIN.arm64"
  build_one amd64 "$BIN.amd64"
  lipo -create -output "$BIN" "$BIN.arm64" "$BIN.amd64"
  rm "$BIN.arm64" "$BIN.amd64"
else
  build_one "$GOARCH" "$BIN"
fi

# Substitute __VERSION__ in the plist template.
sed "s/__VERSION__/$VERSION/g" "$SCRIPT_DIR/Info.plist.tmpl" > "$APP/Contents/Info.plist"

echo
echo "✓ Built $APP (version $VERSION)"
echo "  open $APP"
