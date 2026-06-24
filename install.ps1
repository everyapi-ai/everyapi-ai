#Requires -Version 5.1
<#
.SYNOPSIS
  EveryAPI CLI — one-shot installer for Windows (PowerShell).

.DESCRIPTION
  Windows counterpart to clients/cli/install.sh. Same trust model: resolve a
  release tag, download everyapi_windows_amd64.zip + SHA256SUMS, verify the
  SHA256 (and the cosign keyless signature when cosign is present), then drop
  everyapi.exe on the user's PATH.

  Canonical source:  everyapi-ai/everyapi  -> clients/cli/install.ps1
  Served at:         https://everyapi.ai/install.ps1 (the landing worker
                     proxies the public mirror everyapi-ai/everyapi-ai, which
                     cli-release.yml snapshots clients/cli/ into). Edit the
                     canonical file here; the release pipeline mirrors it.

.EXAMPLE
  irm https://everyapi.ai/install.ps1 | iex

.EXAMPLE
  # Pass options by materializing the script first:
  & ([scriptblock]::Create((irm https://everyapi.ai/install.ps1))) -Version v0.2.2
#>
[CmdletBinding()]
param(
  # Pin a specific release tag (default: latest).
  [string]$Version = '',
  # Install into $Prefix\bin (default: %LOCALAPPDATA%\everyapi\bin).
  [string]$Prefix = '',
  # Reinstall even if the target version is already on disk.
  [switch]$Force,
  # Fail if cosign verification of SHA256SUMS can't run or doesn't pass.
  [switch]$RequireSignature,
  # Skip the cosign step entirely (SHA256 only).
  [switch]$SkipSignature
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 still negotiates TLS 1.0 by default on older boxes;
# GitHub + the release CDN require 1.2+. PS 7 already defaults to the system
# protocols, so this is a no-op there.
try {
  [Net.ServicePointManager]::SecurityProtocol =
    [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
  # Some locked-down hosts forbid touching ServicePointManager — keep going;
  # the download step surfaces a clear TLS error if it actually matters.
}

$Repo = 'everyapi-ai/everyapi-ai'

function Write-Info($m) { Write-Host "> $m" -ForegroundColor Cyan }
function Write-Ok($m)   { Write-Host "+ $m" -ForegroundColor Green }
function Write-Warn($m) { Write-Host "! $m" -ForegroundColor Yellow }

# Fatal errors THROW rather than `exit`: this script is meant to be run as
# `irm ... | iex`, where it executes in the caller's session — an `exit` there
# would close the user's terminal on any failure. The throw is caught by the
# wrapper at the bottom, which prints the message and returns control.
function Die($m) { throw $m }

function Get-LatestTag {
  # Ask the API for the latest release tag. Unlike install.sh (which follows
  # the /releases/latest web redirect to dodge anonymous rate limits), a one-
  # shot installer makes a single call, so the 60/hr anonymous budget is a
  # non-issue and the JSON path needs no cross-version redirect handling.
  $headers = @{ 'User-Agent' = 'everyapi-install.ps1'; 'Accept' = 'application/vnd.github+json' }
  try {
    return (Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers).tag_name
  } catch {
    Die "could not resolve the latest version (is the public mirror up?). Pass -Version vX.Y.Z explicitly. ($($_.Exception.Message))"
  }
}

function Invoke-Install {
  # ----- Platform detection --------------------------------------------------
  #
  # The release ships everyapi_windows_amd64.zip only — no native arm64 build.
  # Windows 11 on ARM runs x64 binaries under emulation, so an arm64 host gets
  # the amd64 artifact with a heads-up rather than a hard failure.
  $archRaw = $env:PROCESSOR_ARCHITECTURE
  if (-not $archRaw) { $archRaw = 'AMD64' }
  switch ($archRaw.ToUpperInvariant()) {
    'AMD64' { $arch = 'amd64' }
    'X86'   { $arch = 'amd64'; Write-Warn '32-bit shell assumed on a 64-bit OS; installing the amd64 build.' }
    'ARM64' { $arch = 'amd64'; Write-Warn 'No native arm64 Windows build yet; installing amd64 (runs under x64 emulation).' }
    default { Die "unsupported arch: $archRaw (expected AMD64 / ARM64)" }
  }
  Write-Ok "platform: windows_$arch"

  # ----- Resolve version -----------------------------------------------------
  $ver = $Version
  if (-not $ver) {
    Write-Info "resolving latest release tag from $Repo..."
    $ver = Get-LatestTag
  }
  if ($ver -notmatch '^v\d') {
    Die "version must start with 'v' (got: $ver)"
  }
  Write-Ok "version: $ver"

  # ----- Resolve install dir -------------------------------------------------
  if ($Prefix) {
    $installDir = Join-Path $Prefix 'bin'
  } else {
    $base = $env:LOCALAPPDATA
    if (-not $base) { $base = Join-Path $HOME 'AppData\Local' }
    $installDir = Join-Path $base 'everyapi\bin'
  }
  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  Write-Ok "install dir: $installDir"

  $target = Join-Path $installDir 'everyapi.exe'

  # Same-version short-circuit (matches install.sh): re-running the one-liner
  # is a no-op once you're current, keeping it safe in setup scripts. -Force
  # overrides for a re-verify / repair.
  if ((Test-Path $target) -and -not $Force) {
    $existing = ''
    try { $existing = (& $target version 2>$null | Select-Object -First 1) } catch { }
    if ($existing) {
      $m = [regex]::Match($existing, '\d+\.\d+\.\d+')
      if ($m.Success -and $m.Value -eq $ver.TrimStart('v')) {
        Write-Ok "already at $ver — nothing to do (pass -Force to reinstall)"
        return
      }
      Write-Info "found existing install: $existing"
    }
  }

  # ----- Download ------------------------------------------------------------
  $zipName = "everyapi_windows_${arch}.zip"
  $sumsName = 'SHA256SUMS'
  $baseUrl = "https://github.com/$Repo/releases/download/$ver"

  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("everyapi-install." + [System.IO.Path]::GetRandomFileName())
  New-Item -ItemType Directory -Force -Path $tmp | Out-Null
  try {
    $zipPath = Join-Path $tmp $zipName
    $sumsPath = Join-Path $tmp $sumsName

    Write-Info "downloading $zipName..."
    try {
      Invoke-WebRequest -Uri "$baseUrl/$zipName" -OutFile $zipPath -UseBasicParsing
    } catch {
      Die "failed to download $baseUrl/$zipName — double-check that $ver is published at https://github.com/$Repo/releases"
    }
    Write-Info "downloading $sumsName..."
    try {
      Invoke-WebRequest -Uri "$baseUrl/$sumsName" -OutFile $sumsPath -UseBasicParsing
    } catch {
      Die "failed to download $baseUrl/$sumsName — refusing to install without a checksum"
    }

    # ----- Verify SHA256 -----------------------------------------------------
    #
    # Pull the single SHA256SUMS line for the artifact we fetched and string-
    # compare against Get-FileHash. The match is anchored to the two-space
    # sha256sum separator + end-of-line so an artifact whose name is a
    # substring of another can't match the wrong row.
    Write-Info 'verifying SHA256...'
    $escaped = [regex]::Escape($zipName)
    $line = (Get-Content -LiteralPath $sumsPath) |
      Where-Object { $_ -match ("^[a-fA-F0-9]{64}  " + $escaped + '$') } |
      Select-Object -First 1
    if (-not $line) {
      Die "$zipName not listed in $sumsName — release artifacts may be incomplete"
    }
    $expected = ($line -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expected -ne $actual) {
      Die 'SHA256 mismatch — refusing to install a tampered or corrupt binary'
    }
    Write-Ok 'SHA256 verified'

    # ----- Verify cosign signature (best-effort or required) -----------------
    #
    # .goreleaser.yml keyless-signs SHA256SUMS via Fulcio + the release
    # workflow's GitHub OIDC identity. When cosign is on PATH we verify it
    # here for provenance; otherwise we skip (SHA256 integrity only) unless
    # -RequireSignature was passed. cosign is rarely installed on Windows, so
    # the default path is integrity-only — the same tradeoff install.sh makes.
    if (-not $SkipSignature) {
      $cosign = Get-Command cosign -ErrorAction SilentlyContinue
      if ($cosign) {
        $sigOk = $false
        try {
          Write-Info 'downloading cosign signature + certificate...'
          Invoke-WebRequest -Uri "$baseUrl/$sumsName.sig" -OutFile "$sumsPath.sig" -UseBasicParsing
          Invoke-WebRequest -Uri "$baseUrl/$sumsName.pem" -OutFile "$sumsPath.pem" -UseBasicParsing
          Write-Info 'verifying cosign signature...'
          # Pin the OIDC issuer to GitHub Actions AND the cert identity to the
          # exact release workflow (cli-release.yml on everyapi-ai/everyapi) —
          # the same identity install.sh pins, so a signature minted by any
          # other workflow is rejected.
          & cosign verify-blob `
            --certificate "$sumsPath.pem" `
            --signature "$sumsPath.sig" `
            --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' `
            --certificate-identity-regexp '^https://github\.com/everyapi-ai/everyapi/\.github/workflows/cli-release\.yml@' `
            "$sumsPath" *> $null
          if ($LASTEXITCODE -eq 0) { $sigOk = $true }
        } catch {
          # fall through to the not-verified handling below
        }
        if ($sigOk) {
          Write-Ok 'cosign signature verified'
        } elseif ($RequireSignature) {
          Die 'cosign signature verification failed and -RequireSignature is set'
        } else {
          Write-Warn 'cosign signature verification failed — proceeding because -RequireSignature was not passed'
        }
      } elseif ($RequireSignature) {
        Die 'cosign is not installed but -RequireSignature was passed. Install cosign from https://github.com/sigstore/cosign and retry.'
      } else {
        Write-Warn 'cosign not installed — skipping signature verify (SHA256 integrity only, no provenance)'
        Write-Warn '  install cosign and rerun with -RequireSignature for cryptographic provenance'
      }
    }

    # ----- Extract + install -------------------------------------------------
    Write-Info 'extracting...'
    $unzipDir = Join-Path $tmp 'unzip'
    Expand-Archive -LiteralPath $zipPath -DestinationPath $unzipDir -Force
    $exe = Get-ChildItem -LiteralPath $unzipDir -Recurse -Filter 'everyapi.exe' | Select-Object -First 1
    if (-not $exe) {
      Die 'archive did not contain everyapi.exe'
    }

    # A running everyapi.exe can lock the destination; surface that as
    # actionable advice rather than a raw access-denied stack.
    try {
      Copy-Item -LiteralPath $exe.FullName -Destination $target -Force
    } catch {
      Die "could not write $target — close any running 'everyapi' process and retry. ($($_.Exception.Message))"
    }
    Write-Ok 'installed'
  } finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }

  $installedVer = ''
  try { $installedVer = (& $target version 2>$null | Select-Object -First 1) } catch { }
  if (-not $installedVer) { $installedVer = $ver }
  Write-Ok $installedVer

  # ----- PATH ----------------------------------------------------------------
  #
  # Persist installDir onto the User PATH when it isn't already there, and add
  # it to the current session so `everyapi` works without reopening the shell.
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not $userPath) { $userPath = '' }
  $onPath = @($userPath -split ';' |
    Where-Object { $_ -and ($_.TrimEnd('\') -ieq $installDir.TrimEnd('\')) }).Count -gt 0
  if (-not $onPath) {
    $newPath = if ($userPath) { "$installDir;$userPath" } else { $installDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$installDir;$env:Path"
    Write-Host ''
    Write-Warn "$installDir was added to your User PATH."
    Write-Host '  Open a new terminal for other apps to pick it up.'
  }

  Write-Host ''
  Write-Host 'Next steps:'
  Write-Host '  - Sign in:        everyapi auth login'
  Write-Host '  - Point a CLI:    everyapi use claude   # or codex / gemini'
  Write-Host '  - Check balance:  everyapi auth status'
  Write-Host '  - Help:           everyapi help'
}

try {
  Invoke-Install
} catch {
  Write-Host "x $($_.Exception.Message)" -ForegroundColor Red
}
