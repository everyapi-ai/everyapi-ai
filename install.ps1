#Requires -Version 5.1
<#
.SYNOPSIS
  EveryAPI CLI — one-shot installer for Windows (PowerShell).

.DESCRIPTION
  Windows counterpart to clients/cli/install.sh. Same trust model: resolve a
  release tag, download everyapi_windows_amd64.zip + SHA256SUMS, verify the
  SHA256 (and the cosign keyless signature when cosign is present), then drop
  everyapi.exe on the user's PATH.

  Canonical source: everyapi-ai/everyapi -> clients/cli/install.ps1
  Distribution:     cli-release.yml publishes this file to the Aliyun OSS
                    mirror on every release, served at
                    https://dl.everyapi.ai/install.ps1 — one command,
                    worldwide (mainland China via the mirror, overseas via
                    GitHub; self-routed at runtime). everyapi.ai/install.ps1
                    is an overseas alias kept in sync. Edit the canonical here.

.EXAMPLE
  irm https://dl.everyapi.ai/install.ps1 | iex

.EXAMPLE
  # Pass options by materializing the script first:
  & ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2
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
$MirrorBase = 'https://dl.everyapi.ai'

function Write-Info($m) { Write-Host "> $m" -ForegroundColor Cyan }
function Write-Ok($m)   { Write-Host "+ $m" -ForegroundColor Green }
function Write-Warn($m) { Write-Host "! $m" -ForegroundColor Yellow }

# Fatal errors THROW rather than `exit`: this script is meant to be run as
# `irm ... | iex`, where it executes in the caller's session — an `exit` there
# would close the user's terminal on any failure. The throw is caught by the
# wrapper at the bottom, which prints the message and returns control.
function Die($m) { throw $m }

function Get-LatestTag {
  # Mirror mode (parity with install.sh): read the tag from a plain-text
  # "<base>/latest" pointer the release pipeline writes next to the artifacts.
  if ($DownloadBase) {
    try {
      return ((Invoke-WebRequest -Uri "$DownloadBase/latest" -TimeoutSec 8 -UseBasicParsing).Content).Trim()
    } catch {
      Die "could not resolve the latest version from mirror ($DownloadBase/latest). Pass -Version vX.Y.Z explicitly. ($($_.Exception.Message))"
    }
  }
  # GitHub mode. NOTE: api.github.com is a DIFFERENT host than the github.com
  # reachability probe and is independently blocked/throttled in mainland China,
  # so a passing probe does not guarantee this resolves. On failure, fall back to
  # the mirror's /latest and route the rest of the install through the mirror too
  # (set $script:DownloadBase) — the parity of install.sh's resolve fallback.
  $headers = @{ 'User-Agent' = 'everyapi-install.ps1'; 'Accept' = 'application/vnd.github+json' }
  try {
    return (Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers -TimeoutSec 8).tag_name
  } catch {
    Write-Warn "github.com version lookup failed — using the mainland mirror ($MirrorBase)"
    $script:DownloadBase = $MirrorBase
    try {
      return ((Invoke-WebRequest -Uri "$MirrorBase/latest" -TimeoutSec 8 -UseBasicParsing).Content).Trim()
    } catch {
      Die "could not resolve the latest version from GitHub or the mirror. Pass -Version vX.Y.Z explicitly. ($($_.Exception.Message))"
    }
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
  $baseUrl = if ($DownloadBase) { "$DownloadBase/$ver" } else { "https://github.com/$Repo/releases/download/$ver" }

  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("everyapi-install." + [System.IO.Path]::GetRandomFileName())
  New-Item -ItemType Directory -Force -Path $tmp | Out-Null
  try {
    $zipPath = Join-Path $tmp $zipName
    $sumsPath = Join-Path $tmp $sumsName

    # Download with parity to install.sh's fetch(): retry the primary a few
    # times (a transient blip on a reachable GitHub shouldn't reroute trust to
    # the mirror), then — only if the primary was GitHub — fall back to the
    # mirror once. The mainland case is a passing github.com probe but a blocked
    # asset host (objects.githubusercontent.com). -TimeoutSec bounds a stalled
    # transfer so it errors (and falls back) instead of hanging. On fallback we
    # flip $script:DownloadBase; $baseUrl is recomputed afterward for cosign.
    function Get-Asset($name, $out) {
      $primary = if ($DownloadBase) { "$DownloadBase/$ver" } else { "https://github.com/$Repo/releases/download/$ver" }
      for ($i = 1; $i -le 3; $i++) {
        try { Invoke-WebRequest -Uri "$primary/$name" -OutFile $out -TimeoutSec 120 -UseBasicParsing; return $true } catch { }
        if ($i -lt 3) { Start-Sleep -Seconds 1 }
      }
      if ($DownloadBase) { return $false }   # already on a mirror — no further fallback
      Write-Warn 'github.com download failed — falling back to the mainland mirror'
      $script:DownloadBase = $MirrorBase
      try { Invoke-WebRequest -Uri "$MirrorBase/$ver/$name" -OutFile $out -TimeoutSec 120 -UseBasicParsing; return $true } catch { return $false }
    }

    Write-Info "downloading $zipName..."
    if (-not (Get-Asset $zipName $zipPath)) {
      Die "failed to download $zipName from GitHub and the mirror — check your connection, or pass -Version vX.Y.Z"
    }
    Write-Info "downloading $sumsName..."
    if (-not (Get-Asset $sumsName $sumsPath)) {
      Die "failed to download $sumsName — refusing to install without a checksum"
    }
    # Whatever source the downloads settled on (a fallback may have flipped it),
    # point $baseUrl there so the cosign sig/cert come from the same place.
    $baseUrl = if ($DownloadBase) { "$DownloadBase/$ver" } else { "https://github.com/$Repo/releases/download/$ver" }

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
        # Download the sig/cert in their OWN try so a MISSING artifact (404,
        # or a release whose .sig/.pem aren't published yet during the
        # mirror-sync window) is reported as "signature not available" —
        # NOT conflated with a signature that exists but fails to verify
        # (which would wrongly imply tampering). Mirrors install.sh's split.
        $sigAvailable = $false
        try {
          Write-Info 'downloading cosign signature + certificate...'
          Invoke-WebRequest -Uri "$baseUrl/$sumsName.sig" -OutFile "$sumsPath.sig" -TimeoutSec 30 -UseBasicParsing
          Invoke-WebRequest -Uri "$baseUrl/$sumsName.pem" -OutFile "$sumsPath.pem" -TimeoutSec 30 -UseBasicParsing
          $sigAvailable = $true
        } catch {
          if ($RequireSignature) {
            Die "cosign signature/certificate not available for $Version and -RequireSignature is set"
          }
          Write-Warn "signature not available for $Version — skipping cosign verify"
        }
        if ($sigAvailable) {
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
          if ($sigOk) {
            Write-Ok 'cosign signature verified'
          } elseif ($RequireSignature) {
            Die 'cosign signature verification failed and -RequireSignature is set'
          } else {
            Write-Warn 'cosign signature verification failed — proceeding because -RequireSignature was not passed'
          }
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
  #
  # Read the RAW (unexpanded) value straight from the registry with
  # DoNotExpandEnvironmentNames, and remember its value KIND. [Environment]::
  # GetEnvironmentVariable expands %VAR% tokens, and [Environment]::
  # SetEnvironmentVariable ALWAYS writes REG_SZ — round-tripping a PATH that
  # was REG_EXPAND_SZ (the Windows default whenever it holds entries like
  # %USERPROFILE%\bin / %JAVA_HOME%\bin) through those APIs would either bake
  # in the expanded values or persist literal %VAR% tokens that no longer
  # expand, silently breaking the user's environment for other tools. So
  # write back through the registry preserving the original type.
  $envKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
  $userPath = ''
  $pathKind = [Microsoft.Win32.RegistryValueKind]::ExpandString
  if ($envKey) {
    $userPath = [string]$envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    try { $pathKind = $envKey.GetValueKind('Path') } catch { }
    $envKey.Close()
  }
  # No existing value: pick the type from the content we're about to write —
  # ExpandString only if it actually contains a %VAR% token.
  if (-not $userPath) {
    $pathKind = [Microsoft.Win32.RegistryValueKind]::String
  }
  $onPath = @($userPath -split ';' |
    Where-Object { $_ -and ($_.TrimEnd('\') -ieq $installDir.TrimEnd('\')) }).Count -gt 0
  if (-not $onPath) {
    $newPath = if ($userPath) { "$installDir;$userPath" } else { $installDir }
    $pathType = if ($pathKind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString) { 'ExpandString' } else { 'String' }
    Set-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value $newPath -Type $pathType
    # Set-ItemProperty (unlike SetEnvironmentVariable) doesn't notify running
    # processes, so broadcast WM_SETTINGCHANGE ourselves — otherwise Explorer
    # and already-open shells wouldn't see the new PATH until the next logon.
    try {
      if (-not ('Win32.NativeMethods' -as [type])) {
        Add-Type -Namespace Win32 -Name NativeMethods -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr hWnd, uint Msg, System.UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out System.UIntPtr lpdwResult);
'@
      }
      $result = [System.UIntPtr]::Zero
      [void][Win32.NativeMethods]::SendMessageTimeout([System.IntPtr]0xffff, 0x1A, [System.UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
    } catch { }
    $env:Path = "$installDir;$env:Path"
    Write-Host ''
    # Parity with install.sh's PATH hint, but Windows lets us DO the fix rather
    # than hand the user a command to paste: the User PATH is now persisted and
    # this session's $env:Path is already updated, so `everyapi` works in this
    # window right away. Only other/already-open apps need a fresh terminal.
    Write-Ok "Setup done: added $installDir to your User PATH (and this session)."
    Write-Host '  everyapi works in this window now; open a new terminal for other apps to pick it up.'
  }

  Write-Host ''
  Write-Host 'Next steps:'
  Write-Host '  - Sign in:        everyapi auth login'
  Write-Host '  - Point a CLI:    everyapi use claude   # or codex / gemini'
  Write-Host '  - Check balance:  everyapi auth status'
  Write-Host '  - Help:           everyapi help'
}

# One command, everywhere (parity with install.sh). EVERYAPI_DOWNLOAD_BASE
# overrides; otherwise probe github.com and fall back to the Aliyun OSS mirror
# when it's unreachable (mainland China). Reachability, not geo-IP — what
# matters is "can this host reach GitHub". A non-empty $DownloadBase makes
# Get-LatestTag + the download step read from the mirror; empty means GitHub.
$DownloadBase = $env:EVERYAPI_DOWNLOAD_BASE
if ($DownloadBase) {
  $DownloadBase = $DownloadBase.TrimEnd('/')
  Write-Info "download source: mirror ($DownloadBase)"
} else {
  try {
    Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" -Method Head -TimeoutSec 4 -UseBasicParsing | Out-Null
    $DownloadBase = ''
  } catch {
    $DownloadBase = $MirrorBase
    Write-Warn "github.com is slow or unreachable — using the mainland mirror ($MirrorBase)"
  }
}

try {
  Invoke-Install
} catch {
  Write-Host "x $($_.Exception.Message)" -ForegroundColor Red
}
