#!/usr/bin/env bash
set -euo pipefail

mirror_base=${CLI_MIRROR_BASE:-https://dl.everyapi.ai/cli-mirrors}
cache_dir=${HERMES_MIRROR_CACHE_DIR:-$HOME/.cache/everyapi/hermes}
install_dir=${HERMES_INSTALL_DIR:-$HOME/.hermes/hermes-agent}
mkdir -p "$cache_dir"

fetch() {
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 10 --max-time 1800 --retry 2 -o "$2" "$1"
}
verify() {
  local file=$1 expected=$2 actual
  [[ $expected =~ ^[0-9a-f]{64}$ ]] || { echo "invalid mirrored Hermes checksum" >&2; return 1; }
  actual=$(shasum -a 256 "$file" | awk '{print $1}')
  [[ $actual == "$expected" ]]
}

commit=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 30 "$mirror_base/hermes/latest" | tr -d '[:space:]')
[[ $commit =~ ^[0-9a-f]{40}$ ]] || { echo "invalid mirrored Hermes commit" >&2; exit 1; }

bundle="$cache_dir/$commit.bundle"
installer="$cache_dir/$commit-install.sh"
fetch "$mirror_base/hermes/$commit/install.sh" "$installer"
bundle_sha=$(curl -fsSL "$mirror_base/hermes/$commit/hermes-agent.bundle.sha256" | tr -d '[:space:]')
installer_sha=$(curl -fsSL "$mirror_base/hermes/$commit/install.sh.sha256" | tr -d '[:space:]')
[[ $bundle_sha =~ ^[0-9a-f]{64}$ ]] || { echo "invalid mirrored Hermes checksum" >&2; exit 1; }
if [[ ! -s $bundle ]] || ! verify "$bundle" "$bundle_sha"; then
  bundle_download="$bundle.download"
  fetch "$mirror_base/hermes/$commit/hermes-agent.bundle" "$bundle_download"
  verify "$bundle_download" "$bundle_sha" || { echo "checksum mismatch for mirrored Hermes bundle" >&2; exit 1; }
  mv -f "$bundle_download" "$bundle"
fi
verify "$installer" "$installer_sha" || { echo "checksum mismatch for mirrored Hermes installer" >&2; exit 1; }
git bundle list-heads "$bundle" | grep -q 'refs/heads/main$' || { echo "mirrored Hermes bundle has no main branch" >&2; exit 1; }

# Existing mirrored installs should fetch the newly downloaded bundle. A fresh
# install receives the same URL through the reviewed upstream installer's
# normal clone path.
bundle_url=$bundle
official_origin=https://github.com/NousResearch/hermes-agent.git
restore_origin_url=$official_origin
if [[ -d $install_dir/.git ]]; then
  current_origin=$(git -C "$install_dir" remote get-url origin 2>/dev/null || true)
  if [[ -n $current_origin && $current_origin != "$cache_dir/"*.bundle ]]; then
    restore_origin_url=$current_origin
  fi
fi
restore_origin() {
  [[ -d $install_dir/.git ]] || return 0
  git -C "$install_dir" remote set-url origin "$restore_origin_url" || {
    echo "could not restore Hermes origin to $restore_origin_url" >&2
    return 1
  }
}
trap 'restore_origin || true' EXIT
if [[ -d $install_dir/.git ]]; then
  git -C "$install_dir" remote set-url origin "$bundle_url"
fi
patched="$cache_dir/$commit-install.patched.sh"
ssh_assignment='REPO_URL_SSH="git@github.com:NousResearch/hermes-agent.git"'
https_assignment='REPO_URL_HTTPS="https://github.com/NousResearch/hermes-agent.git"'
[[ $(grep -Fxc "$ssh_assignment" "$installer" || true) == 1 ]] || { echo "Hermes SSH clone contract changed" >&2; exit 1; }
[[ $(grep -Fxc "$https_assignment" "$installer" || true) == 1 ]] || { echo "Hermes HTTPS clone contract changed" >&2; exit 1; }
awk -v bundle="$bundle_url" -v uv="$mirror_base/uv/install.sh" \
  -v ssh="$ssh_assignment" -v https="$https_assignment" '
    $0 == ssh { print "REPO_URL_SSH=\"" bundle "\""; next }
    $0 == https { print "REPO_URL_HTTPS=\"" bundle "\""; next }
    { gsub("https://astral.sh/uv/install.sh", uv); print }
  ' "$installer" >"$patched"
chmod 0700 "$patched"

export UV_DEFAULT_INDEX=${UV_DEFAULT_INDEX:-https://mirrors.aliyun.com/pypi/simple/}
export UV_PYTHON_INSTALL_MIRROR=${UV_PYTHON_INSTALL_MIRROR:-$mirror_base/python}
installer_status=0
bash "$patched" "$@" || installer_status=$?
trap - EXIT
restore_origin || exit 1
exit "$installer_status"
