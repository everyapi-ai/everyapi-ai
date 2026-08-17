param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('antigravity', 'crush', 'goose', 'forge', 'librefang')]
    [string]$Tool
)

$ErrorActionPreference = 'Stop'
$MirrorBase = if ($env:CLI_MIRROR_BASE) { $env:CLI_MIRROR_BASE.TrimEnd('/') } else { 'https://dl.everyapi.ai/cli-mirrors' }
$InstallDir = if ($env:CLI_MIRROR_INSTALL_DIR) { $env:CLI_MIRROR_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'EveryAPI\bin' }

if (-not [Environment]::Is64BitOperatingSystem) {
    throw "$Tool mirror supports 64-bit Windows only."
}

$Targets = @($Tool)
if ($Tool -eq 'forge') { $Targets += @('fzf', 'bat', 'fd') }
$Stage = Join-Path ([IO.Path]::GetTempPath()) ('everyapi-cli-' + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $Stage | Out-Null
$Artifacts = @{}
try {
    # Fetch and verify every artifact before mutating the install directory.
    foreach ($Target in $Targets) {
        $Version = (Invoke-RestMethod "$MirrorBase/$Target/latest").Trim()
        if ($Version -notmatch '^v?[0-9]+\.[0-9]+\.[0-9]+$') {
            throw "Invalid mirrored $Target version."
        }
        $PlatformBase = "$MirrorBase/$Target/$Version/windows-x64"
        $BinaryName = if ($Target -eq 'antigravity') { 'agy.exe' } else { "$Target.exe" }
        $Artifact = Join-Path $Stage "$Target.exe"
        Invoke-WebRequest "$PlatformBase/binary.exe" -OutFile $Artifact
        $Expected = (Invoke-RestMethod "$PlatformBase/sha256").Trim().ToLowerInvariant()
        if ($Expected -notmatch '^[0-9a-f]{64}$') { throw "Invalid mirrored $Target checksum." }
        $Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Artifact).Hash.ToLowerInvariant()
        if ($Actual -ne $Expected) { throw "Checksum mismatch for mirrored $Target." }
        $Artifacts[$Target] = @{ Path = $Artifact; Name = $BinaryName; Version = $Version }
    }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    foreach ($Target in $Targets) {
        $Item = $Artifacts[$Target]
        Copy-Item -LiteralPath $Item.Path -Destination (Join-Path $InstallDir $Item.Name) -Force
        Write-Host "Installed $Target $($Item.Version) to $InstallDir"
    }
}
finally {
    Remove-Item -LiteralPath $Stage -Recurse -Force -ErrorAction SilentlyContinue
}

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$Parts = @($UserPath -split ';' | Where-Object { $_ })
if ($Parts -notcontains $InstallDir) {
    [Environment]::SetEnvironmentVariable('Path', (($Parts + $InstallDir) -join ';'), 'User')
}
