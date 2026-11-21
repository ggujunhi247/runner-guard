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
    Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt.sig" -OutFile (Join-Path $tmp 'checksums.txt.sig')
    # The release key (private half kept off GitHub); same as internal/update.PublicKey.
    $p = New-Object Security.Cryptography.RSAParameters
    $p.Modulus = [Convert]::FromBase64String('4xuHI4gBNCLStJKC0Fc2FdwhBJleuR7p9awiT8d8TXJO1zZaWowAhz8nLIwq02NBRXdC8X6MMeBgqA194LlbpoZk0AulasLIfSLpCq9GfPEjUUfIlwVOoeouROVl2VQJPZs7Sy/iffFaux5uOPJG9cLj5XqHiy5q6UnJey7kwyOl7Bi/rblULz95dF37WTmh7Gxikkh5cmXUPig6D82cT02YoEvKOLUK6m94KqA1HJ+wLpVrjPQA52u8dyAtr6Dl+ajlnkZH7Pq1tQB4C5RCh1NJz75mpBupKgnTqzmrzFEcUZZC3K6pVejeZnBYZRY7j8UCzSqZgyNLBIY+9hLo/wIqG4XYPDJ/bVS2vcRAex6YmbnxA9Ur3/14ZBayfEliuIkegDOgm0cza7RuqHK2NxtdN4ChKEFWJgjm+Sw7ZMAUDPkTUrmQ29Pyd5HFmwRfWgF9RjEbmD6A5Jf5sgOOcZuuc5mziBKQAeDhjaT5nShJ9VkxdIep5m3vx8Cs5bn5')
    $p.Exponent = [Convert]::FromBase64String('AQAB')
    $rsa = New-Object Security.Cryptography.RSACryptoServiceProvider
    $rsa.ImportParameters($p)
    $sums = [IO.File]::ReadAllBytes((Join-Path $tmp 'checksums.txt'))
    $sig = [IO.File]::ReadAllBytes((Join-Path $tmp 'checksums.txt.sig'))
    if (-not $rsa.VerifyData($sums, 'SHA256', $sig)) { throw 'runner-guard install: release signature does not match; not installing' }
    # Strict sha256sum format, exactly one entry: a line other tools would skip must not pick the hash.
    $all = [IO.File]::ReadAllText((Join-Path $tmp 'checksums.txt')).TrimEnd("`n").Split("`n")
    if ($all | Where-Object { $_ -cnotmatch '^[0-9a-f]{64} [ *]\S+$' }) { throw 'runner-guard install: malformed checksums.txt' }
    $line = @($all | Where-Object { $_ -cmatch ('^[0-9a-f]{64} [ *]' + [regex]::Escape($asset) + '$') })
    if ($line.Count -ne 1) { throw "runner-guard install: expected one checksum for $asset, found $($line.Count)" }
    $line = $line[0]
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
