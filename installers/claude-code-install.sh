#!/usr/bin/env bash
set -euo pipefail

# Minimal, repository-reviewed bootstrap for the Claude Code artifacts mirrored
# by EveryAPI. The sync job accepts a binary only after verifying Anthropic's
# signed manifest; this script verifies the published SHA256 again before use.

target=${1:-latest}
mirror_base=${CLAUDE_MIRROR_BASE:-https://dl.everyapi.ai/claude-code}
[[ $target =~ ^(stable|latest|[0-9]+\.[0-9]+\.[0-9]+)$ ]] || {
  echo "Usage: $0 [stable|latest|VERSION]" >&2
  exit 2
}
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64|Darwin-aarch64) platform=darwin-arm64 ;;
  *) echo 'The Claude Code mirror supports Apple Silicon macOS only.' >&2; exit 1 ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
version=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 30 "$mirror_base/latest" | tr -d '[:space:]')
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid mirrored Claude Code version.' >&2; exit 1; }

curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 1800 --retry 2 \
  -o "$stage/claude" "$mirror_base/$version/$platform/claude"
expected=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 30 "$mirror_base/$version/$platform/sha256" | tr -d '[:space:]')
[[ $expected =~ ^[0-9a-f]{64}$ ]] || { echo 'Invalid mirrored Claude Code checksum.' >&2; exit 1; }
actual=$(shasum -a 256 "$stage/claude" | awk '{print $1}')
[[ $actual == "$expected" ]] || { echo 'Claude Code checksum mismatch.' >&2; exit 1; }
chmod +x "$stage/claude"
"$stage/claude" install "$target"
