# runner-guard helper for Windows runner hosts. Guide: docs/windows.md
# Run in an elevated PowerShell opened as the user Docker Desktop runs under.
#   .\rg-windows.ps1 help
# If scripts are blocked:  powershell -ExecutionPolicy Bypass -File .\rg-windows.ps1 <command>
param(
    [Parameter(Position = 0)][string]$Command = 'help',
    [Parameter(Position = 1)][string]$Arg1,
    [Parameter(Position = 2)][string]$Arg2
)
$ErrorActionPreference = 'Stop'
$dir = Join-Path $env:ProgramData 'runner-guard'
$rg = Join-Path $dir 'runner-guard.exe'
$cfgPath = Join-Path $dir 'config.json'
$tasks = 'runner-guard-run', 'runner-guard-update', 'runner-guard-sample'
$installUrl = 'https://raw.githubusercontent.com/ggujunhi247/runner-guard/main/install.ps1'

function Test-Admin {
    $p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Assert-Admin { if (-not (Test-Admin)) { throw 'Run this in an elevated (Administrator) PowerShell.' } }
function Assert-Installed { if (-not (Test-Path $rg)) { throw "runner-guard is not installed ($rg). Run: .\rg-windows.ps1 install" } }
function Invoke-RG { Assert-Installed; & $rg @args; if ($LASTEXITCODE) { exit $LASTEXITCODE } }

function Read-Config {
    if (Test-Path $cfgPath) {
        $c = Get-Content $cfgPath -Raw | ConvertFrom-Json
        if ($c) { return $c }
    }
    New-Object psobject
}
# UTF-8 without BOM: PowerShell 5's Out-File/Set-Content would write UTF-16 or ANSI.
function Write-Config($c) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    [IO.File]::WriteAllText($cfgPath, ($c | ConvertTo-Json), (New-Object Text.UTF8Encoding $false))
}
# 120 -> number, true -> bool, ["a"] -> list; anything else stays text.
function ConvertTo-Value([string]$s) {
    try { $v = $s | ConvertFrom-Json } catch { return $s }
    if ($s.TrimStart().StartsWith('[')) { return , ([object[]]@($v)) } # keep a one-item list a list
    $v
}

function Show-Check([bool]$ok, [string]$what, [string]$fix) {
    if ($ok) { Write-Host "  ok    $what" } else { Write-Host "  FIX   $what -> $fix" -ForegroundColor Yellow; $script:bad++ }
}

function Invoke-Preflight {
    $script:bad = 0
    Write-Host 'Checking this host for runner-guard:'
    Show-Check (Test-Admin) 'elevated PowerShell' 'reopen PowerShell with "Run as administrator"'
    $auto = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon' -ErrorAction SilentlyContinue).AutoAdminLogon
    Show-Check ($auto -eq '1') 'automatic logon (tasks and Docker Desktop need a logged-on user)' 'Sysinternals Autologon, or: .\rg-windows.ps1 set reboot_recover false'
    $docker = $false
    try { docker info --format '{{.ServerVersion}}' 2>$null | Out-Null; $docker = ($LASTEXITCODE -eq 0) } catch {}
    Show-Check $docker 'Docker answers' 'start Docker Desktop; turn on "Start Docker Desktop when you sign in"'
    $svc = (Read-Config).runner_service
    if (-not $svc) { $svc = 'gitlab-runner' }
    Show-Check ([bool](Get-Service -Name $svc -ErrorAction SilentlyContinue)) "runner service '$svc' exists" 'install gitlab-runner, or: .\rg-windows.ps1 set runner_service <name>'
    # Output is localized; the first index line is the AC setting (0 = never).
    $idx = powercfg /query SCHEME_CURRENT SUB_SLEEP STANDBYIDLE 2>$null | Select-String '0x[0-9a-fA-F]{8}\s*$' | Select-Object -First 1
    Show-Check ($idx -and $idx.Line -match '0x00000000\s*$') 'sleep on AC power is off' '.\rg-windows.ps1 nosleep'
    if (Test-Path $rg) { Write-Host "  info  installed: $(& $rg version)" } else { Write-Host '  info  runner-guard not installed yet' }
    if ($bad) { Write-Host "$bad item(s) to fix." -ForegroundColor Yellow } else { Write-Host 'All good.' }
}

function Get-State {
    $p = Join-Path $dir 'state.json'
    if (Test-Path $p) { Get-Content $p -Raw | ConvertFrom-Json }
}

switch ($Command) {
    'preflight' { Invoke-Preflight }
    'nosleep' {
        Assert-Admin
        powercfg /change standby-timeout-ac 0
        powercfg /change hibernate-timeout-ac 0
        powercfg /hibernate off
        Write-Host 'Sleep and hibernate are off.'
    }
    'install' {
        Assert-Admin
        if ($Arg1) { $env:RG_VERSION = $Arg1 } else { Remove-Item Env:RG_VERSION -ErrorAction SilentlyContinue }
        Invoke-Expression (Invoke-RestMethod -UseBasicParsing $installUrl)
        Write-Host 'Next: .\rg-windows.ps1 status   (fix any PROBLEM lines before the first run, 15 min after install)'
    }
    'status' { Invoke-RG status }
    'run' { Assert-Admin; Invoke-RG run }
    'tune' { Invoke-RG tune }
    'tune-reset' { Assert-Admin; Invoke-RG tune --reset }
    'tune-off' { Assert-Admin; New-Item -ItemType File -Force (Join-Path $dir 'tune.disabled') | Out-Null; Write-Host 'Auto-tune now only reports.' }
    'tune-on' { Assert-Admin; Remove-Item (Join-Path $dir 'tune.disabled') -ErrorAction SilentlyContinue; Write-Host 'Auto-tune is on.' }
    'log' {
        $n = 50; if ($Arg1) { $n = [int]$Arg1 }
        Get-Content (Join-Path $dir 'runner-guard.log') -Tail $n
    }
    'update' { Assert-Admin; Invoke-RG update }
    'check-update' { Invoke-RG update --check }
    'config' { if (Test-Path $cfgPath) { Get-Content $cfgPath -Raw } else { Write-Host "No $cfgPath yet; all defaults." } }
    'set' {
        Assert-Admin
        if (-not $Arg1) { throw 'usage: .\rg-windows.ps1 set <key> <value>   e.g. set desktop_budget_gb 120' }
        $c = Read-Config
        $c | Add-Member -Force -NotePropertyName $Arg1 -NotePropertyValue (ConvertTo-Value $Arg2)
        Write-Config $c
        Get-Content $cfgPath -Raw
    }
    'unset' {
        Assert-Admin
        $c = Read-Config
        $c.PSObject.Properties.Remove($Arg1)
        Write-Config $c
        Get-Content $cfgPath -Raw
    }
    'pause' {
        Assert-Admin
        $st = Get-State
        if ($st -and $st.blocked_by_us) {
            Write-Warning "runner-guard has the runner stopped ($($st.block_reason)). Paused, nothing will start it again until 'resume'."
        }
        foreach ($t in $tasks) { schtasks /Change /TN $t /DISABLE | Out-Null }
        Write-Host 'runner-guard tasks disabled. Undo with: .\rg-windows.ps1 resume'
    }
    'resume' {
        Assert-Admin
        foreach ($t in $tasks) { schtasks /Change /TN $t /ENABLE | Out-Null }
        Write-Host 'runner-guard tasks enabled.'
    }
    'tasks' {
        foreach ($t in $tasks) {
            schtasks /Query /TN $t /V /FO LIST | Select-String 'TaskName|Status|Run As User|Next Run Time|Last Result'
        }
    }
    'uninstall' { Assert-Admin; Invoke-RG uninstall }
    default {
        @'
runner-guard helper (guide: docs/windows.md in github.com/ggujunhi247/runner-guard)

  preflight            check this host (auto logon, Docker, runner service, sleep)
  nosleep              turn off sleep and hibernate
  install [vX.Y.Z]     install, reinstall or upgrade (latest, or that version)
  status               state and PROBLEM lines
  log [N]              last N log lines (50)
  tasks                scheduled task status
  run                  run the hourly check now
  tune | tune-reset    show what auto-tune would change | forget learned values
  tune-off | tune-on   auto-tune reports only | applies again
  config               show config.json
  set <key> <value>    e.g. set desktop_budget_gb 120 / set reboot_recover false
                       set alert_webhook https://... / set refuse_if_exists '["C:\\marker"]'
  unset <key>          back to the default
  pause | resume       disable | enable the tasks during maintenance
  check-update | update
  uninstall
'@ | Write-Host
    }
}
