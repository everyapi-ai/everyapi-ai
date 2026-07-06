#!/usr/bin/env bash
# EveryAPI CLI — one-shot installer for Linux / macOS.
#
# Canonical source: everyapi-ai/everyapi → clients/cli/install.sh
#
# Distribution: cli-release.yml publishes this file (plus the release
# artifacts and a `latest` pointer) to the Aliyun OSS mirror on every
# release, served at https://dl.everyapi.ai/install.sh — the one install
# command, worldwide. It works from mainland China (the OSS mirror) and
# overseas (GitHub), self-routing the artifact downloads at runtime, so
# there is no separate "China command".
#
# everyapi-ai/everyapi-web also serves a verbatim copy at
# https://everyapi.ai/install.sh (marketing site, overseas alias). Because
# this is a curl|bash installer — arbitrary code piped into a shell —
# drift is a supply-chain risk: edit the canonical here first, then mirror
# it byte-for-byte into everyapi-web.
#
# Usage:
#
#   # Latest stable, smart prefix (~/.local/bin for non-root, /usr/local/bin for root)
#   curl -fsSL https://dl.everyapi.ai/install.sh | bash
#
#   # Pin a specific version
#   curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2
#
#   # Force a specific prefix (script picks the bin/ subdir under it)
#   curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local
#
# What it does:
#
#   1. Detects OS (linux / darwin) + arch (amd64 / arm64).
#   2. Resolves the latest v* tag from the public release mirror
#      (or honours --version vX.Y.Z).
#   3. Downloads everyapi_{os}_{arch}.tar.gz + SHA256SUMS into a temp dir.
#   4. Verifies the SHA256 against the checksum file.
#   5. If cosign is installed, also verifies the keyless signature on
#      SHA256SUMS (matches the cosign verify flow documented in
#      .goreleaser.yml — provenance proof, not just integrity).
#   6. Extracts everyapi and installs it to the resolved prefix's bin/.
#   7. Prints a PATH-export hint if the install dir isn't on $PATH yet.
#
# Idempotent: re-running with the latest tag upgrades in place when a
# newer release is out, and short-circuits with a "already at vX.Y.Z"
# message when the installed version matches the target. Pass --force
# to override the no-op and reinstall the same version on top of itself
# (useful for verifying binary integrity or recovering a damaged file).
#
# Why a curl-pipe-bash installer and not just "go grab the tarball":
# Linux distros don't have a homebrew-equivalent the way macOS does,
# and `go install` requires the user to install Go first. This script
# is the missing third install path that the README's Installation
# section now points to as the Linux primary.

set -euo pipefail

# Wrap the entire installer in a function and invoke it as the very
# last line. `curl … | bash` reads bytes through a pipe; if the
# connection drops mid-stream bash may execute whatever lines it
# already received. With the body in a function, a truncated download
# defines a partial function and exits cleanly when bash hits EOF
# without ever calling `main` — no half-installed binary, no half-
# written checksum file, no half-replaced existing install.
main() {

# ----- Defaults --------------------------------------------------------------

REPO="everyapi-ai/everyapi-ai"
VERSION=""
VERSION_PINNED=0   # 1 when --version was passed: never silently substitute it
DOWNLOAD_LOCKED=0  # 1 once the first artifact is downloaded: freeze (source,version) so the checksum can't come from a different tag
PREFIX=""
FORCE=0
VERIFY_SIGNATURE="auto"   # auto | required | skip
COSIGN_VERIFIED=0         # 1 once cosign keyless verification actually passes

# ----- Pretty print ----------------------------------------------------------

if [ -t 1 ]; then
  BOLD=$(printf '\033[1m'); GREEN=$(printf '\033[32m')
  YELLOW=$(printf '\033[33m'); RED=$(printf '\033[31m')
  RESET=$(printf '\033[0m')
else
  BOLD=""; GREEN=""; YELLOW=""; RED=""; RESET=""
fi
info() { printf '%b▶%b %s\n' "$BOLD" "$RESET" "$*" >&2; }
ok()   { printf '%b✓%b %s\n' "$GREEN" "$RESET" "$*" >&2; }
warn() { printf '%b!%b %s\n' "$YELLOW" "$RESET" "$*" >&2; }
err()  { printf '%b✗%b %s\n' "$RED" "$RESET" "$*" >&2; }

# ----- Args ------------------------------------------------------------------

# need_value validates that the value-taking flag $1 has a non-empty
# argument at $2. Without this, `--version` as the last argv element
# would trip `set -u` and exit with a cryptic "$2: unbound variable"
# message that points at the case clause rather than the user's bad
# invocation. Returns 0 if there's a value; emits an error and signals
# failure (return 1) otherwise — caller exits to keep the case
# arms readable.
need_value() {
  if [ "$2" -lt 2 ] || [ -z "${3:-}" ]; then
    err "$1 requires a value"
    err "run with --help for usage"
    return 1
  fi
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      need_value "$1" "$#" "${2:-}" || exit 1
      VERSION="$2"; VERSION_PINNED=1; shift 2 ;;
    --prefix)
      need_value "$1" "$#" "${2:-}" || exit 1
      PREFIX="$2"; shift 2 ;;
    --force)              FORCE=1; shift ;;
    --require-signature)  VERIFY_SIGNATURE="required"; shift ;;
    --skip-signature)     VERIFY_SIGNATURE="skip"; shift ;;
    -h|--help)
      # Help text is inlined (not extracted from the script's leading
      # comment block) so it works under `curl … | bash -s -- --help`,
      # where $0 is "bash" rather than a readable script path. Keep
      # this synopsis in sync with the doc comment at the top of the
      # file when adding new flags.
      cat <<'HELP'
EveryAPI CLI installer — Linux / macOS

Usage:
  curl -fsSL https://dl.everyapi.ai/install.sh | bash
  curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- [options]

Options:
  --version vX.Y.Z       Pin a specific release tag (default: latest).
  --prefix DIR           Install into DIR/bin (default: ~/.local/bin,
                         or /usr/local/bin when running as root).
  --force                Reinstall even if the target version is already
                         on disk.
  --require-signature    Fail if cosign keyless verification of
                         SHA256SUMS can't run or doesn't pass.
  --skip-signature       Skip the cosign step entirely (SHA256 only).
  -h, --help             Show this help.

What it does:
  1. Detects OS (linux / darwin) and arch (amd64 / arm64).
  2. Resolves the latest v* tag (or honours --version).
  3. Downloads everyapi_{os}_{arch}.tar.gz + SHA256SUMS.
  4. Verifies the SHA256 against the checksum file.
  5. If cosign is installed, also verifies the keyless signature on
     SHA256SUMS (provenance proof, not just integrity).
  6. Atomically installs the binary into the resolved prefix's bin/.
  7. Prints a PATH-export hint if the install dir isn't on $PATH.

Re-running the same command checks for a newer release and upgrades
in place when one exists; it's a no-op when the installed version
already matches. Pass --force to reinstall on top of itself.
HELP
      exit 0
      ;;
    *)
      err "unknown arg: $1"
      err "run with --help for usage"
      exit 1
      ;;
  esac
done

# ----- Prerequisites ---------------------------------------------------------

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "$1 is required but not installed"
    return 1
  fi
}

info "checking prerequisites…"
require curl || exit 1
require tar  || exit 1
# Either sha256sum (linux coreutils) or shasum (macOS / BSD) will do —
# we branch on which one exists at verify time.
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  err "neither sha256sum nor shasum is available — install coreutils (linux) or perl (mac fallback)"
  exit 1
fi
ok "curl + tar + sha256 tool present"

# ----- Platform detection ----------------------------------------------------

UNAME_S=$(uname -s)
UNAME_M=$(uname -m)
case "$UNAME_S" in
  Linux)   OS="linux" ;;
  Darwin)  OS="darwin" ;;
  *)
    err "unsupported OS: $UNAME_S (expected Linux or Darwin)"
    err "for Windows, download everyapi_windows_amd64.zip from https://github.com/$REPO/releases manually"
    exit 1
    ;;
esac
case "$UNAME_M" in
  x86_64|amd64)         ARCH="amd64" ;;
  aarch64|arm64)        ARCH="arm64" ;;
  *)
    err "unsupported arch: $UNAME_M (expected x86_64 / amd64 / aarch64 / arm64)"
    exit 1
    ;;
esac
ok "platform: ${OS}_${ARCH}"

# ----- Choose download source + resolve version ------------------------------
#
# One command, everywhere. EVERYAPI_DOWNLOAD_BASE (if set) forces a mirror.
# Otherwise GitHub Releases is the default, with the built-in Aliyun OSS mirror
# as the fallback for mainland China (where github.com is slow/blocked). We
# decide by REACHABILITY, not geo-IP — resolving the latest tag from GitHub
# doubles as the reachability test, so the happy path is a single request and a
# GitHub failure (timeout/blocked) drops transparently to the mirror's
# "<base>/latest" pointer. A non-empty DOWNLOAD_BASE routes downloads through
# "<base>/<tag>/"; empty means GitHub. Every network call here is time-bounded
# so a throttled-to-a-trickle CN link errors (and falls back) instead of
# hanging forever.
MIRROR_BASE="https://dl.everyapi.ai"

# gh_latest: echo the latest v* tag via the github.com web redirect (NOT
# api.github.com — that path is separately rate-limited/blocked), or empty on
# any failure/timeout. /releases/latest 302s to /releases/tag/<tag>; we read the
# Location with %{url_effective} and strip to the tag.
gh_latest() {
  local _url
  # --proto/--proto-redir '=https': this follows the releases/latest ->
  # releases/tag redirect, so pin BOTH the initial request and every
  # redirect hop to https (a redirect to http:// would otherwise be
  # followed in cleartext).
  _url=$(curl -fsSLI --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 5 --max-time 8 -o /dev/null \
    -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null || true)
  case "$_url" in
    */releases/tag/v*) printf '%s' "${_url##*/}" ;;
  esac
}

# mirror_latest: echo the latest tag from the mirror's plain-text /latest, or
# empty. Used for mirror-mode resolution and the fetch() release-window fallback.
mirror_latest() {
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 5 --max-time 8 \
    "$MIRROR_BASE/latest" 2>/dev/null | head -n1 | tr -d '[:space:]' || true
}

if [ -n "${EVERYAPI_DOWNLOAD_BASE:-}" ]; then
  DOWNLOAD_BASE="${EVERYAPI_DOWNLOAD_BASE%/}"
  info "download source: mirror (EVERYAPI_DOWNLOAD_BASE=$DOWNLOAD_BASE)"
  if [ -z "$VERSION" ]; then
    info "resolving latest release tag from mirror…"
    VERSION=$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 5 --max-time 8 \
      "$DOWNLOAD_BASE/latest" 2>/dev/null | head -n1 | tr -d '[:space:]' || true)
    if [ -z "$VERSION" ]; then
      err "could not resolve latest version from mirror ($DOWNLOAD_BASE/latest)"
      err "try passing --version vX.Y.Z explicitly"
      exit 1
    fi
  fi
elif [ -n "$VERSION" ]; then
  # Version pinned — no resolution needed. Pick a source with a quick (timed)
  # github.com reachability probe; fetch() still falls back per download.
  if curl -fsS --connect-timeout 5 --max-time 8 -o /dev/null -I \
       "https://github.com/$REPO/releases/latest" 2>/dev/null; then
    DOWNLOAD_BASE=""
    info "download source: GitHub Releases"
  else
    DOWNLOAD_BASE="$MIRROR_BASE"
    warn "github.com is slow or unreachable — using the mainland mirror ($MIRROR_BASE)"
  fi
else
  # Resolve the latest tag; a successful resolve doubles as the reachability
  # probe (one request, not two), and a failure drops to the mirror.
  info "resolving latest release tag…"
  VERSION=$(gh_latest)
  if [ -n "$VERSION" ]; then
    DOWNLOAD_BASE=""
    info "download source: GitHub Releases ($VERSION)"
  else
    DOWNLOAD_BASE="$MIRROR_BASE"
    warn "github.com is slow or unreachable — using the mainland mirror ($MIRROR_BASE)"
    VERSION=$(mirror_latest)
    if [ -z "$VERSION" ]; then
      err "could not resolve latest version from GitHub or the mirror"
      err "try passing --version vX.Y.Z explicitly"
      exit 1
    fi
  fi
fi
case "$VERSION" in
  v*) ;;
  *)
    err "version must start with 'v' (got: $VERSION)"
    exit 1
    ;;
esac
ok "version: $VERSION"

# ----- Resolve install prefix ------------------------------------------------

# Smart-prefix selection: explicit --prefix wins; otherwise root /
# sudo install gets /usr/local/bin (system-wide), and ordinary user
# installs get ~/.local/bin (XDG convention — no sudo needed, and
# `everyapi update` will later detect it isn't under a managed prefix
# and fall back to the manual hint, but that's fine for a fresh
# install). EUID is used over `whoami = root` so SUDO_USER edge cases
# don't accidentally write to the calling user's home.
if [ -n "$PREFIX" ]; then
  INSTALL_DIR="$PREFIX/bin"
elif [ "${EUID:-$(id -u)}" -eq 0 ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
fi
ok "install dir: $INSTALL_DIR"

# Create the install dir up front so we fail loudly here (permission
# denied is easier to diagnose than a write failure at the very end
# after we've already downloaded + verified the tarball).
mkdir -p "$INSTALL_DIR"

# Detect existing install and short-circuit on same-version re-runs.
# Curl|bash semantics: a user re-running the one-liner should be a
# no-op when they're already current — that's what makes the command
# safe to put in setup scripts / dotfiles. --force overrides the
# no-op for cases where the binary is suspected corrupt or the user
# wants to re-verify the signature chain.
#
# We parse the binary's `version` subcommand output ("everyapi X.Y.Z
# (commit …)") with grep -oE to lift the bare semver, then compare
# against ${VERSION#v}. A binary too old to ship `version` returns
# empty here — falls through to a normal install, which is the right
# thing (the old binary predates the safe-upgrade path anyway).
if [ -x "$INSTALL_DIR/everyapi" ]; then
  EXISTING_VER=$("$INSTALL_DIR/everyapi" version 2>/dev/null | head -n1 || true)
  if [ -n "$EXISTING_VER" ]; then
    info "found existing install: $EXISTING_VER"
    EXISTING_SEMVER=$(printf '%s\n' "$EXISTING_VER" \
      | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true)
    TARGET_SEMVER="${VERSION#v}"
    if [ -n "$EXISTING_SEMVER" ] && \
       [ "$EXISTING_SEMVER" = "$TARGET_SEMVER" ] && \
       [ "$FORCE" -ne 1 ]; then
      ok "already at $VERSION — nothing to do (pass --force to reinstall)"
      exit 0
    fi
  fi
fi

# ----- Download --------------------------------------------------------------

TARBALL="everyapi_${OS}_${ARCH}.tar.gz"
SUMS="SHA256SUMS"
if [ -n "$DOWNLOAD_BASE" ]; then
  BASE_URL="$DOWNLOAD_BASE/$VERSION"
else
  BASE_URL="https://github.com/$REPO/releases/download/$VERSION"
fi

# Explicit-path mktemp invocation. `mktemp -d -t TEMPLATE` is one of
# the few mktemp flags that splits hard between GNU and BSD: GNU treats
# the template as a literal name; BSD treats it as a prefix and appends
# its own X-suffix. Passing the full path skips the disagreement —
# both implementations accept it the same way, and we keep control of
# the parent directory ($TMPDIR if set, /tmp otherwise).
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/everyapi-install.XXXXXX")
# Single trap covering both success and failure paths so a download
# error doesn't leave the tmpdir behind to accumulate under /tmp.
trap 'rm -rf "$TMP_DIR"' EXIT

# dl: curl with the download hardening shared by every fetch. --connect-timeout
# bounds the handshake; --speed-limit/--speed-time abort a transfer that drops
# below 1 KB/s for 20s, so a throttled-to-a-trickle CN link ERRORS (and triggers
# the retry/fallback below) instead of hanging forever — there is deliberately
# no overall --max-time, since a large binary on a slow-but-healthy link is fine.
dl() {
  # --proto-redir '=https': --proto only constrains the FIRST hop; with -L
  # a redirect to http:// would otherwise be followed in cleartext, letting
  # a redirect-based downgrade MITM the artifact/checksum on any hop.
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 10 --speed-limit 1024 --speed-time 20 "$@"
}

# fetch <name> <outfile>: download $BASE_URL/<name>. Retry the primary source a
# few times first — a transient blip on a REACHABLE GitHub should not silently
# reroute trust to the mirror — then, only if the primary was GitHub, fall back
# to the mirror once. That fallback is the mainland case where the github.com
# probe passed but the asset host (objects.githubusercontent.com) is blocked.
# On fallback DOWNLOAD_BASE/BASE_URL flip to the mirror so the checksum + cosign
# fetches that follow stay consistent; and if the mirror doesn't have the
# GitHub-resolved tag yet (decoupled mirror job still running after a release),
# it drops to the mirror's own latest instead of 404-ing.
fetch() {
  local _name="$1" _out="$2" _try
  for _try in 1 2 3; do
    if dl -o "$_out" "$BASE_URL/$_name"; then
      return 0
    fi
    [ "$_try" -lt 3 ] && sleep 1
  done
  [ -n "$DOWNLOAD_BASE" ] && return 1   # already on a mirror — nothing to fall back to
  warn "github.com download failed — falling back to the mainland mirror"
  DOWNLOAD_BASE="$MIRROR_BASE"
  BASE_URL="$MIRROR_BASE/$VERSION"
  if dl -o "$_out" "$BASE_URL/$_name"; then
    return 0
  fi
  [ "$VERSION_PINNED" -eq 1 ] && return 1   # explicit --version: don't substitute
  # Once the FIRST artifact (the tarball) is committed, freeze the version:
  # substituting a different tag here would fetch SHA256SUMS for a tag that
  # doesn't match the already-downloaded tarball, producing a bogus "SHA256
  # mismatch — tampered binary" abort during a release/mirror-sync window
  # (the two artifacts were each authentic, just for different tags).
  [ "$DOWNLOAD_LOCKED" -eq 1 ] && return 1
  local _ml
  _ml=$(mirror_latest)
  if [ -n "$_ml" ] && [ "$_ml" != "$VERSION" ]; then
    warn "mirror has no $VERSION yet — using the mirror's latest ($_ml)"
    VERSION="$_ml"
    BASE_URL="$MIRROR_BASE/$VERSION"
    dl -o "$_out" "$BASE_URL/$_name"
    return $?
  fi
  return 1
}

info "downloading ${TARBALL}…"
fetch "$TARBALL" "$TMP_DIR/$TARBALL" || {
  err "failed to download $TARBALL from GitHub and the mirror"
  err "check your connection, or pin a known version with --version vX.Y.Z"
  exit 1
}
# The tarball's (source, version) is now committed. Freeze it so the
# checksum + signature that follow are fetched for the SAME tag — see the
# DOWNLOAD_LOCKED note in fetch(). fetch() may have already rewritten
# VERSION/BASE_URL if the tarball itself fell back to the mirror's latest;
# locking here keeps SHA256SUMS aligned with whatever the tarball ended up being.
DOWNLOAD_LOCKED=1
info "downloading ${SUMS}…"
fetch "$SUMS" "$TMP_DIR/$SUMS" || {
  err "failed to download $SUMS — refusing to install without checksum"
  exit 1
}

# ----- Verify SHA256 ---------------------------------------------------------
#
# Filter the checksum file down to the single line for the artifact
# we downloaded, then feed it to the platform's sha256 verifier. We
# don't verify the whole file because release archives we didn't
# fetch (other platforms') would fail with "no such file" and abort
# a verification that should have passed.
#
# The filter is fully anchored: 64 lowercase hex chars, exactly two
# spaces (sha256sum's literal separator), then the artifact name and
# end-of-line. A plain `grep -F "  $TARBALL"` would match any line
# where the filename appears as a substring — fine today since the
# release manifest doesn't have artifacts whose names contain other
# artifact names, but a future addition (e.g. shipping a separate
# `.tar.gz.sig` listed in SHA256SUMS) would silently match two lines.
# Anchoring keeps the contract narrow.

info "verifying SHA256…"
# Escape dots in the artifact name so they match literally in the ERE
# below. Filenames like `everyapi_linux_amd64.tar.gz` only contain `.`
# as an ERE metacharacter; underscores and alphanumerics are inert.
TARBALL_RE=$(printf '%s' "$TARBALL" | sed 's/\./\\./g')
ARTIFACT_LINE=$(grep -E "^[a-f0-9]{64}  ${TARBALL_RE}$" "$TMP_DIR/$SUMS" || true)
if [ -z "$ARTIFACT_LINE" ]; then
  err "$TARBALL not listed in $SUMS — release artifacts may be incomplete"
  exit 1
fi

# Compute the digest ourselves and string-compare, rather than piping
# the checksum line to `sha256sum -c` / `shasum -c`. The `-c` checkfile
# mode is a portability minefield: GNU coreutils, Apple's native BSD
# /sbin/sha256sum (shipped since macOS 26), busybox, and perl shasum
# each accept different flags. In particular `--status` is GNU-only —
# Apple's tool rejects it, prints "usage: sha256sum [-bctwz] …" and
# exits non-zero, which the old code misread as a checksum mismatch and
# aborted a perfectly good download. Every one of these tools, however,
# prints "HASH␣␣FILENAME" when simply handed a file, so taking the first
# whitespace field is universally safe.
EXPECTED=$(printf '%s\n' "$ARTIFACT_LINE" | awk '{print $1}')
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP_DIR/$TARBALL" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "$TMP_DIR/$TARBALL" | awk '{print $1}')
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
  err "SHA256 mismatch — refusing to install a tampered or corrupt binary"
  exit 1
fi
ok "SHA256 verified"

# ----- Verify cosign signature (best-effort or required) ---------------------
#
# .goreleaser.yml keyless-signs SHA256SUMS via Fulcio + the workflow's
# GitHub OIDC identity. When cosign is installed we verify that
# signature here — it pairs the checksum trust above with cryptographic
# proof of provenance ("the checksum file was produced by the official
# release pipeline on everyapi-ai/everyapi"). When cosign isn't
# installed we skip with a hint, unless --require-signature was passed
# (CI / supply-chain-sensitive environments can use that to fail loudly
# instead of silently skipping).

if [ "$VERIFY_SIGNATURE" != "skip" ]; then
  if command -v cosign >/dev/null 2>&1; then
    info "downloading cosign signature + certificate…"
    dl -o "$TMP_DIR/$SUMS.sig" "$BASE_URL/$SUMS.sig" || {
      if [ "$VERIFY_SIGNATURE" = "required" ]; then
        err "signature download failed and --require-signature is set"
        exit 1
      fi
      warn "signature not available for $VERSION — skipping cosign verify"
    }
    if [ -f "$TMP_DIR/$SUMS.sig" ]; then
      dl -o "$TMP_DIR/$SUMS.pem" "$BASE_URL/$SUMS.pem" || {
        if [ "$VERIFY_SIGNATURE" = "required" ]; then
          err "certificate download failed and --require-signature is set"
          exit 1
        fi
        warn "certificate not available for $VERSION — skipping cosign verify"
      }
    fi
    if [ -f "$TMP_DIR/$SUMS.sig" ] && [ -f "$TMP_DIR/$SUMS.pem" ]; then
      info "verifying cosign signature…"
      # Pin the OIDC issuer to GitHub Actions AND the cert identity to
      # the exact workflow that mints releases — cli-release.yml on
      # everyapi-ai/everyapi. The earlier org-wide pin let any actor
      # with workflow permissions on any everyapi-ai repo produce a
      # passing signature; the narrow pin rejects everything but the
      # release pipeline itself. The "exposing the workflow path leaks
      # private-repo state" argument that justified the broader pin
      # doesn't hold: the Fulcio cert's SAN already encodes this exact
      # path (visible to anyone who decodes SHA256SUMS.pem), so the
      # path isn't a secret. Regex anchors at the @ separator so any
      # future ref (workflow_dispatch off a non-main branch) still
      # verifies — what we lock down is the workflow file, not the
      # ref that ran it.
      if cosign verify-blob \
          --certificate "$TMP_DIR/$SUMS.pem" \
          --signature "$TMP_DIR/$SUMS.sig" \
          --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
          --certificate-identity-regexp "^https://github\.com/everyapi-ai/everyapi/\.github/workflows/cli-release\.yml@" \
          "$TMP_DIR/$SUMS" >/dev/null 2>&1; then
        COSIGN_VERIFIED=1
        ok "cosign signature verified"
      else
        if [ "$VERIFY_SIGNATURE" = "required" ]; then
          err "cosign signature verification failed and --require-signature is set"
          exit 1
        fi
        warn "cosign signature verification failed — proceeding because --require-signature was not passed"
      fi
    fi
  else
    if [ "$VERIFY_SIGNATURE" = "required" ]; then
      err "cosign is not installed but --require-signature was passed"
      err "install cosign from https://github.com/sigstore/cosign and retry"
      exit 1
    fi
    # Default install path: cosign isn't installed, so we proceed
    # with SHA256 integrity only — no cryptographic proof that the
    # checksum file itself was produced by the release pipeline.
    # `warn` (not `info`) because that tradeoff matters in a curl|bash
    # installer; a user skimming output should see this is a degraded
    # mode, not a clean success. Pass --require-signature to fail
    # loudly instead.
    warn "cosign not installed — skipping signature verify (SHA256 integrity only, no provenance)"
    warn "  install cosign and rerun with --require-signature for cryptographic provenance"
  fi
fi

# Same-origin checksum is INTEGRITY, not AUTHENTICITY. When the binary was
# served from the mirror (mainland auto-fallback or an explicit
# EVERYAPI_DOWNLOAD_BASE), SHA256SUMS came from that same origin, so a
# matching digest proves only that the mirror agrees with itself — a
# compromised mirror can serve a backdoored tarball plus a matching
# checksum. cosign keyless verification is the ONLY control here that
# proves the release came from the official pipeline. Surface that plainly
# when we're about to install a mirror-served binary without a cosign proof.
if [ -n "$DOWNLOAD_BASE" ] && [ "$COSIGN_VERIFIED" -ne 1 ] && [ "$VERIFY_SIGNATURE" != "skip" ]; then
  warn "──────────────────────────────────────────────────────────────"
  warn "DEGRADED TRUST: installing a mirror-served binary ($DOWNLOAD_BASE)"
  warn "with SHA256 integrity only. The checksum was fetched from the SAME"
  warn "origin as the binary, so it is NOT proof of authenticity."
  warn "For cryptographic provenance: install cosign and re-run with"
  warn "--require-signature, or install from GitHub directly."
  warn "──────────────────────────────────────────────────────────────"
fi

# ----- Extract + install -----------------------------------------------------

info "extracting…"
# --no-same-owner: GoReleaser packs archives as root:root. Without
# this flag GNU tar tries to preserve that ownership when extraction
# runs as root, which is rarely what an installer wants (the binary
# should end up owned by the running user, not whatever uid the build
# host happened to use). Both GNU tar and macOS bsdtar (libarchive)
# accept the flag — bsdtar treats it as a compat alias.
tar --no-same-owner -xzf "$TMP_DIR/$TARBALL" -C "$TMP_DIR"
if [ ! -f "$TMP_DIR/everyapi" ]; then
  err "tarball did not contain an 'everyapi' binary at its root"
  exit 1
fi
chmod 755 "$TMP_DIR/everyapi"

# Atomic install: write next to the target then rename. install(1)
# isn't universally available (busybox / minimal containers), and a
# direct cp can leave a half-written file at the destination if the
# disk fills mid-copy. The two-step write + rename keeps the existing
# binary intact until the new one is fully on disk.
TARGET="$INSTALL_DIR/everyapi"
STAGE="$INSTALL_DIR/everyapi.install.$$"
info "installing to $TARGET"
cp "$TMP_DIR/everyapi" "$STAGE"
chmod 755 "$STAGE"
mv -f "$STAGE" "$TARGET"
ok "installed"

# ----- Post-install ----------------------------------------------------------

INSTALLED_VER=$("$TARGET" version 2>/dev/null | head -n1 || echo "$VERSION")
ok "$INSTALLED_VER"

# PATH hint: only emit if the install dir is NOT already on $PATH.
# `case` over `:$PATH:` matches any colon-bounded segment, which is
# more reliable than `[[ $PATH == *"$INSTALL_DIR"* ]]` (that one
# false-positives on e.g. /opt/.local/bin when INSTALL_DIR is
# ~/.local/bin).
case ":${PATH:-}:" in
  *":$INSTALL_DIR:"*) ON_PATH=1 ;;
  *)                  ON_PATH=0 ;;
esac

echo
if [ "$ON_PATH" -eq 0 ]; then
  warn "Setup needed: $INSTALL_DIR is not on your PATH yet."
  # Pick the right rc file hint based on the user's shell. We default
  # to ~/.bashrc when $SHELL is empty or unrecognised — that's the
  # most common case on Linux servers.
  RC_HINT="~/.bashrc"
  case "${SHELL:-}" in
    */zsh)  RC_HINT="~/.zshrc" ;;
    */fish) RC_HINT="~/.config/fish/config.fish" ;;
  esac
  # Emit a single copy-paste command that both persists the PATH change
  # (appends to the rc file) AND applies it to the current shell — so a
  # user can run one line and have `everyapi` work immediately, instead
  # of hand-editing a dotfile and remembering to re-source it. fish gets
  # its idiomatic `fish_add_path` (universal + persistent in one call);
  # bash/zsh get the `echo … >> rc && source rc` idiom. $INSTALL_DIR is
  # baked in literally; \$PATH stays unexpanded so it re-evaluates when
  # the rc file is sourced.
  echo "  Run this to fix it (adds it to $RC_HINT and applies it now):"
  echo
  case "${SHELL:-}" in
    */fish) echo "    fish_add_path $INSTALL_DIR" ;;
    *)      echo "    echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> $RC_HINT && source $RC_HINT" ;;
  esac
  echo
fi

echo "Next steps:"
echo "  • Sign in:        everyapi auth login"
echo "  • Point a CLI:    everyapi use claude   # or codex / gemini"
echo "  • Check balance:  everyapi auth status"
echo "  • Help:           everyapi help"

} # end main

main "$@"
