param([switch]$NonInteractive = $true)

$ErrorActionPreference = 'Stop'
$MirrorBase = if ($env:CLI_MIRROR_BASE) { $env:CLI_MIRROR_BASE.TrimEnd('/') } else { 'https://dl.everyapi.ai/cli-mirrors' }
$CacheDir = if ($env:HERMES_MIRROR_CACHE_DIR) { $env:HERMES_MIRROR_CACHE_DIR } else { Join-Path $env:LOCALAPPDATA 'EveryAPI\cache\hermes' }
$InstallDir = if ($env:HERMES_INSTALL_DIR) { $env:HERMES_INSTALL_DIR } else { Join-Path $env:USERPROFILE '.hermes\hermes-agent' }
New-Item -ItemType Directory -Force -Path $CacheDir | Out-Null

$Commit = (Invoke-RestMethod "$MirrorBase/hermes/latest").Trim()
if ($Commit -notmatch '^[0-9a-f]{40}$') { throw 'Invalid mirrored Hermes commit.' }
$Bundle = Join-Path $CacheDir "$Commit.bundle"
$InstallerPath = Join-Path $CacheDir "$Commit-install.ps1"
$BundleWasCached = Test-Path -LiteralPath $Bundle -PathType Leaf
if (-not (Test-Path -LiteralPath $Bundle -PathType Leaf)) {
    Invoke-WebRequest "$MirrorBase/hermes/$Commit/hermes-agent.bundle" -OutFile $Bundle
}
Invoke-WebRequest "$MirrorBase/hermes/$Commit/install.ps1" -OutFile $InstallerPath

function Assert-Checksum([string]$Path, [string]$Url) {
    $Expected = (Invoke-RestMethod $Url).Trim().ToLowerInvariant()
    if ($Expected -notmatch '^[0-9a-f]{64}$') { throw 'Invalid mirrored Hermes checksum.' }
    $Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) { throw 'Checksum mismatch for mirrored Hermes payload.' }
}
$BundleValid = $true
try {
    Assert-Checksum $Bundle "$MirrorBase/hermes/$Commit/hermes-agent.bundle.sha256"
} catch {
    $BundleValid = $false
    if (-not $BundleWasCached) { throw }
}
if (-not $BundleValid) {
    $BundleDownload = "$Bundle.download"
    Invoke-WebRequest "$MirrorBase/hermes/$Commit/hermes-agent.bundle" -OutFile $BundleDownload
    Assert-Checksum $BundleDownload "$MirrorBase/hermes/$Commit/hermes-agent.bundle.sha256"
    Move-Item -Force -LiteralPath $BundleDownload -Destination $Bundle
}
Assert-Checksum $InstallerPath "$MirrorBase/hermes/$Commit/install.ps1.sha256"
$BundleHeads = & git bundle list-heads $Bundle
if ($LASTEXITCODE -ne 0 -or $BundleHeads -notmatch 'refs/heads/main') { throw 'Mirrored Hermes Git bundle has no main branch.' }

$BundleUrl = $Bundle
$OfficialOrigin = 'https://github.com/NousResearch/hermes-agent.git'
$RestoreOriginUrl = $OfficialOrigin
if (Test-Path -LiteralPath (Join-Path $InstallDir '.git') -PathType Container) {
    $CurrentOrigin = (& git -C $InstallDir remote get-url origin 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -eq 0 -and $CurrentOrigin -and -not $CurrentOrigin.StartsWith(($CacheDir.TrimEnd('\') + '\'), [StringComparison]::OrdinalIgnoreCase)) {
        $RestoreOriginUrl = $CurrentOrigin
    }
}

function Restore-HermesOrigin {
    if (-not (Test-Path -LiteralPath (Join-Path $InstallDir '.git') -PathType Container)) { return }
    & git -C $InstallDir remote set-url origin $RestoreOriginUrl
    if ($LASTEXITCODE -ne 0) { throw "Could not restore Hermes origin to $RestoreOriginUrl." }
}

$Source = Get-Content -LiteralPath $InstallerPath -Raw
$SshLine = '$RepoUrlSsh = "git@github.com:NousResearch/hermes-agent.git"'
$HttpsLine = '$RepoUrlHttps = "https://github.com/NousResearch/hermes-agent.git"'
if (([regex]::Matches($Source, [regex]::Escape($SshLine))).Count -ne 1 -or ([regex]::Matches($Source, [regex]::Escape($HttpsLine))).Count -ne 1) {
    throw 'Hermes repository installer contract changed.'
}
$Source = $Source.Replace($SshLine, ('$RepoUrlSsh = "' + $BundleUrl + '"'))
$Source = $Source.Replace($HttpsLine, ('$RepoUrlHttps = "' + $BundleUrl + '"'))
$Source = $Source.Replace('https://astral.sh/uv/install.ps1', "$MirrorBase/uv/install.ps1")
$Source = $Source.Replace('https://github.com/astral-sh/uv/releases/latest/download/uv-installer.ps1', "$MirrorBase/uv/install.ps1")
$env:UV_DEFAULT_INDEX = if ($env:UV_DEFAULT_INDEX) { $env:UV_DEFAULT_INDEX } else { 'https://mirrors.aliyun.com/pypi/simple/' }
$env:UV_PYTHON_INSTALL_MIRROR = if ($env:UV_PYTHON_INSTALL_MIRROR) { $env:UV_PYTHON_INSTALL_MIRROR } else { "$MirrorBase/python" }
$Installer = [scriptblock]::Create($Source)
try {
    if (Test-Path -LiteralPath (Join-Path $InstallDir '.git') -PathType Container) {
        & git -C $InstallDir remote set-url origin $BundleUrl
        if ($LASTEXITCODE -ne 0) { throw 'Could not point the existing Hermes checkout at its mirror bundle.' }
    }
    & $Installer -NonInteractive:$NonInteractive
} finally {
    Restore-HermesOrigin
}
