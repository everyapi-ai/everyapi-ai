param(
    [Parameter(Position = 0)]
    [ValidatePattern('^(stable|latest|\d+\.\d+\.\d+)$')]
    [string]$Target = 'latest'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$MirrorBase = if ($env:CLAUDE_MIRROR_BASE) { $env:CLAUDE_MIRROR_BASE.TrimEnd('/') } else { 'https://dl.everyapi.ai/claude-code' }

if (-not [Environment]::Is64BitOperatingSystem -or $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
    throw 'The Claude Code mirror supports x64 Windows only.'
}

$Version = (Invoke-RestMethod "$MirrorBase/latest").Trim()
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid mirrored Claude Code version.' }
$Stage = Join-Path ([IO.Path]::GetTempPath()) ('everyapi-claude-' + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $Stage | Out-Null
try {
    $Binary = Join-Path $Stage 'claude.exe'
    $PlatformBase = "$MirrorBase/$Version/win32-x64"
    Invoke-WebRequest "$PlatformBase/claude.exe" -OutFile $Binary
    $Expected = (Invoke-RestMethod "$PlatformBase/sha256").Trim().ToLowerInvariant()
    if ($Expected -notmatch '^[0-9a-f]{64}$') { throw 'Invalid mirrored Claude Code checksum.' }
    $Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Binary).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) { throw 'Claude Code checksum mismatch.' }
    & $Binary install $Target
    if ($LASTEXITCODE -ne 0) { throw "Claude Code installation failed with exit code $LASTEXITCODE." }
}
finally {
    Remove-Item -LiteralPath $Stage -Recurse -Force -ErrorAction SilentlyContinue
}
