# Install, reinstall or upgrade runner-guard from GitHub Releases (Windows).
# Run in an elevated PowerShell opened as the user Docker Desktop runs under:
#   irm https://raw.githubusercontent.com/ggujunhi247/runner-guard/main/install.ps1 | iex
# Pin a release first with:  $env:RG_VERSION = 'v0.6.0'
# The existing config, state and history are kept.
$ErrorActionPreference = 'Stop'
$repo = 'ggujunhi247/runner-guard'
$asset = 'runner-guard_windows_amd64.exe'

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'runner-guard install: run in an elevated (Administrator) PowerShell.'
}
if ($env:RG_VERSION) {
    $base = "https://github.com/$repo/releases/download/$($env:RG_VERSION)"
} else {
    $base = "https://github.com/$repo/releases/latest/download"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ('runner-guard-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $ProgressPreference = 'SilentlyContinue'
    $exe = Join-Path $tmp $asset
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $exe
    Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match ('\s' + [regex]::Escape($asset) + '$') }
    if (-not $line) { throw "runner-guard install: no checksum for $asset" }
    $want = ($line -split '\s+')[0]
    $got = (Get-FileHash -Algorithm SHA256 -Path $exe).Hash
    if ($got -ne $want) { throw "runner-guard install: checksum mismatch for $asset" }
    & $exe selftest
    if ($LASTEXITCODE -ne 0) { throw 'runner-guard install: selftest failed' }
    & $exe install
    if ($LASTEXITCODE -ne 0) { throw 'runner-guard install: install failed' }
    $installed = Join-Path $env:ProgramData 'runner-guard\runner-guard.exe'
    Write-Host "runner-guard $(& $installed version) installed; check: & `"$installed`" status"
} finally {
    Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
}
