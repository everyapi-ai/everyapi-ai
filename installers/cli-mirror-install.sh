#!/usr/bin/env bash
set -euo pipefail

# Installs a reviewed native CLI artifact mirrored from its upstream GitHub
# release. The synchronizer verifies GitHub's release-asset SHA256 before
# publishing; this installer verifies the mirrored bytes again before use.

tool=${1:-}
mirror_base=${CLI_MIRROR_BASE:-https://dl.everyapi.ai/cli-mirrors}
install_dir=${CLI_MIRROR_INSTALL_DIR:-$HOME/.local/bin}

case "$tool" in
  antigravity|crush|goose|openhands|forge|librefang) ;;
  *) echo "unsupported mirrored CLI: $tool" >&2; exit 2 ;;
esac

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64|Darwin-aarch64) platform=darwin-arm64 ;;
  *) echo "$tool mirror supports Apple Silicon macOS; use the official installer on this platform" >&2; exit 1 ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
targets=("$tool")
[[ $tool == forge ]] && targets+=(fzf bat fd)
# Download and verify the complete set before touching the install directory.
for target in "${targets[@]}"; do
  version=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 10 --max-time 30 "$mirror_base/$target/latest" | tr -d '[:space:]')
  if [[ ! $version =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "invalid mirrored $target version" >&2
    exit 1
  fi
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 10 --max-time 1800 --retry 2 \
    -o "$stage/$target" "$mirror_base/$target/$version/$platform/binary"
  expected=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 10 --max-time 30 "$mirror_base/$target/$version/$platform/sha256" | tr -d '[:space:]')
  if [[ ! $expected =~ ^[0-9a-f]{64}$ ]]; then
    echo "invalid mirrored $target checksum" >&2
    exit 1
  fi
  actual=$(shasum -a 256 "$stage/$target" | awk '{print $1}')
  if [[ $actual != "$expected" ]]; then
    echo "checksum mismatch for mirrored $target" >&2
    exit 1
  fi
  printf '%s\n' "$version" >"$stage/$target.version"
done

mkdir -p "$install_dir"
for target in "${targets[@]}"; do
  binary_name=$target
  [[ $target == antigravity ]] && binary_name=agy
  install -m 0755 "$stage/$target" "$install_dir/$binary_name"
  echo "Installed $target $(<"$stage/$target.version") to $install_dir/$binary_name"
done
