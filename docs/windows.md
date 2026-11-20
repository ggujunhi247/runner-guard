# Windows 러너 호스트: 설치와 운영

Docker Desktop과 GitLab Runner(docker executor)가 도는 Windows 호스트에 runner-guard를 설치하고 운영하는 순서입니다. 명령은 모두 복사해서 그대로 씁니다. 동작 설명은 [README](../README.md)에 있습니다.

명령마다 어디서 실행하는지 적었습니다.
- **관리자 PowerShell**: Docker Desktop을 쓰는 그 사용자로 로그인한 뒤, "관리자 권한으로 실행"으로 연 PowerShell입니다.
- 다른 관리자 계정으로 권한을 올려서 설치하면 안 됩니다. 예약 작업이 로그인하지 않는 그 계정에 등록되어 돌지 않습니다.

## 1. 설치 전 준비 (한 번)

### 1-1. 러너 사용자가 늘 로그인해 있게 하기

Docker Desktop과 runner-guard의 예약 작업은 그 사용자 세션 안에서만 돕니다. 재부팅 뒤에도 자동으로 로그인되어야 합니다.

- 자동 로그온은 Microsoft Sysinternals [Autologon](https://learn.microsoft.com/sysinternals/downloads/autologon)으로 켭니다. 비밀번호가 레지스트리에 평문으로 남지 않고 LSA 비밀로 저장됩니다.
- 켜졌는지 확인합니다. `1`이면 됩니다.

  ```powershell
  (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon').AutoAdminLogon
  ```
- 자동 로그온을 켤 수 없는 호스트라면, 2-2에서 `reboot_recover`를 `false`로 두세요. 재부팅해도 아무것도 Docker를 다시 띄우지 못하기 때문입니다.

### 1-2. 절전과 최대 절전 끄기 (관리자 PowerShell)

```powershell
powercfg /change standby-timeout-ac 0
powercfg /change hibernate-timeout-ac 0
powercfg /hibernate off
```

### 1-3. Docker Desktop과 러너 확인

- Docker Desktop 설정에서 **Start Docker Desktop when you sign in**을 켭니다.
- 아래 두 명령이 모두 답하면 됩니다.

  ```powershell
  docker info --format '{{.ServerVersion}}'
  sc.exe query gitlab-runner
  ```
- 러너 서비스 이름이 `gitlab-runner`가 아니면 2-2에서 `runner_service`를 바꿉니다.
- 러너 설정 파일(`config.toml`)은 `C:\GitLab-Runner\config.toml`에 없어도 서비스 명령줄에서 찾습니다.

## 2. 설치 (관리자 PowerShell)

### 2-1. 설치

```powershell
irm https://raw.githubusercontent.com/ggujunhi247/runner-guard/main/install.ps1 | iex
```

- 스크립트가 하는 일: GitHub Releases에서 바이너리를 받습니다. SHA256과 `selftest`로 확인한 뒤, `%ProgramData%\runner-guard`에 설치하고 예약 작업 네 개를 등록합니다.
- 다시 실행하면 그 자리에서 재설치합니다. 설정, 상태, 사용량 기록은 그대로 남습니다.
- 버전을 고정하려면 먼저 `$env:RG_VERSION = 'v0.8.0'`을 실행하고, 같은 줄을 실행합니다.

첫 `run`은 설치 15분 뒤입니다. 그 전에 2-2와 2-3을 끝내세요.

### 2-2. 설정 바꾸기

설정 파일은 `%ProgramData%\runner-guard\config.json`이고, 적은 키만 기본값을 덮어씁니다. 메모장이나 `Out-File`로 저장하면 UTF-16이 될 수 있으니 아래 함수를 쓰세요. UTF-8(BOM 없음)로 저장합니다.

```powershell
function Set-RGConfig([string]$Key, $Value) {
    $p = "$env:ProgramData\runner-guard\config.json"
    $c = if (Test-Path $p) { Get-Content $p -Raw | ConvertFrom-Json } else { New-Object psobject }
    if (-not $c) { $c = New-Object psobject }
    $c | Add-Member -Force -NotePropertyName $Key -NotePropertyValue $Value
    [IO.File]::WriteAllText($p, ($c | ConvertTo-Json), (New-Object Text.UTF8Encoding $false))
    Get-Content $p -Raw
}
```

자주 바꾸는 값:

```powershell
Set-RGConfig alert_webhook 'https://hooks.slack.com/services/...'  # 알림. URL은 비밀이니 여기에만 둡니다
Set-RGConfig desktop_budget_gb 120        # Docker 데이터에 허용할 용량(GB). status의 디스크 %가 이 값 기준
Set-RGConfig reboot_recover $false         # 자동 로그온이 없거나, 재부팅이 곤란한 호스트
Set-RGConfig runner_service 'gitlab-runner-2'   # 서비스 이름이 다를 때
Set-RGConfig auto_tune $false              # 자동 조정 없이 제안만 받기
```

전체 키는 README의 [설정](../README.md#설정) 표에 있습니다. 바꾼 값은 다음 예약 실행부터 쓰입니다. 재시작은 필요 없습니다.

### 2-3. 확인

```powershell
$rg = "$env:ProgramData\runner-guard\runner-guard.exe"
& $rg status
schtasks /Query /TN runner-guard-run /V /FO LIST | Select-String 'Run As User|Status|Next Run'
```

- `status` 끝에 `PROBLEM:` 줄이 없어야 합니다. 있으면 그 줄이 시키는 대로 고칩니다. [문제 해결](#4-문제-해결)도 보세요.
- "Run As User"가 Docker Desktop을 쓰는 사용자여야 합니다.
- 바로 한 번 돌려 보려면 `& $rg run`을 실행합니다. 매시간 하는 일을 지금 한 번 합니다. 디스크가 기준 미만이면 아무것도 지우지 않습니다.

## 3. 평소 운영

이후 명령은 모두 `$rg = "$env:ProgramData\runner-guard\runner-guard.exe"`를 먼저 실행했다고 가정합니다. `status`, `tune`, `check`는 아무것도 바꾸지 않습니다.

| 하려는 일 | 명령 |
| --- | --- |
| 상태 보기 | `& $rg status` (`--json`도 됨) |
| 최근 로그 | `Get-Content "$env:ProgramData\runner-guard\runner-guard.log" -Tail 50` |
| 자동 조정이 무엇을 바꿀지 | `& $rg tune` |
| 배운 조정 값 지우기(멈춘 항목 풀기 포함) | `& $rg tune --reset` |
| 자동 조정 잠시 끄기 / 다시 켜기 | `New-Item "$env:ProgramData\runner-guard\tune.disabled"` / `Remove-Item "$env:ProgramData\runner-guard\tune.disabled"` |
| 새 버전 확인 | `& $rg update --check` |
| 지금 업데이트 | `& $rg update` (보통은 매일 자동) |
| 특정 버전으로 | `$env:RG_VERSION='v0.8.0'; irm https://raw.githubusercontent.com/ggujunhi247/runner-guard/main/install.ps1 \| iex` |
| 예약 작업 상태 | `schtasks /Query /TN runner-guard-run; schtasks /Query /TN runner-guard-sample; schtasks /Query /TN runner-guard-update` |

### 점검 중에 runner-guard 멈추기

호스트를 손볼 때 runner-guard가 러너를 멈추거나 Docker를 재시작하지 않게 하려면, 먼저 `status`에서 러너가 runner-guard 때문에 멈춘 상태가 아닌지 봅니다. 그 상태에서 작업을 끄면 아무도 러너를 다시 켜지 않습니다. 그다음 작업을 끕니다.

```powershell
foreach ($t in 'runner-guard-run','runner-guard-update','runner-guard-sample') { schtasks /Change /TN $t /DISABLE }
# 점검이 끝나면
foreach ($t in 'runner-guard-run','runner-guard-update','runner-guard-sample') { schtasks /Change /TN $t /ENABLE }
```

### 알림을 받으면

| 알림 | 할 일 |
| --- | --- |
| `stopped gitlab-runner (disk: ...)` | 디스크가 `block_percent` 이상입니다. 사용률이 `prune_percent` 아래로 내려가면 자동으로 다시 켭니다. 계속 높으면 Docker 밖의 데이터를 지우거나 `desktop_budget_gb`를 늘립니다 |
| `stopped gitlab-runner (docker: ...)` | Docker가 답하지 않습니다. Docker 재시작, WSL 재시작, 재부팅 순으로 스스로 복구를 시도합니다. 6시간마다 `... still stopped by runner-guard`로 다시 알립니다 |
| `... rebooting in 2 minutes` 뒤 소식이 없음 | 자동 로그온이 안 됐을 수 있습니다. 콘솔에서 로그인합니다. 재부팅은 7일에 한 번까지만 합니다 |
| `tuned ...` | 자동 조정이 값을 하나 바꿨습니다. 72시간 안에 문제가 보이면 스스로 되돌립니다 |
| `tune ... FAILED` / `frozen` | 그 항목의 자동 조정이 멈췄습니다. 로그를 보고 원인을 고친 뒤 `& $rg tune --reset` |

## 4. 문제 해결

| `status`의 PROBLEM / 증상 | 원인과 해결 |
| --- | --- |
| `disk ...% >= block_percent` | 첫 `run`이 러너를 멈출 상황입니다. 정리하거나 `Set-RGConfig desktop_budget_gb <더 큰 값>` |
| `reboot_recover is on but automatic logon is off` | 1-1의 자동 로그온을 켜거나 `Set-RGConfig reboot_recover $false` |
| `no usage sample in the last 30 min` | `runner-guard-sample` 작업이 안 돕니다. 로그인 상태와 "Run As User"를 확인하고 설치 스크립트를 다시 실행 |
| `cannot read concurrent from ...` | `Set-RGConfig runner_config 'D:\GitLab-Runner\config.toml'`처럼 실제 경로를 적습니다 |
| `... tuning is frozen` | 로그에서 이유를 본 뒤 `& $rg tune --reset` |
| `config.json ... UTF-16` 오류 | 2-2의 `Set-RGConfig`로 아무 키나 한 번 저장하면 UTF-8로 다시 씁니다 |
| `another runner-guard is running` | 다른 실행이 끝나기를 기다리는 정상 상태입니다. 그 프로세스가 죽었으면 다음 실행이 잠금을 넘겨받습니다 |
| 예약 작업이 돌지 않음 | 러너 사용자가 로그인해 있는지 봅니다. `schtasks /Query /TN runner-guard-run /V /FO LIST`의 "Last Result"도 봅니다 |

Windows Defender나 Smart App Control이 exe를 막으면 README의 [해당 절](../README.md#windows-defender--smart-app-control)을 보세요.

## 5. 제거 (관리자 PowerShell)

```powershell
& "$env:ProgramData\runner-guard\runner-guard.exe" uninstall
```

- 예약 작업을 지웁니다. runner-guard가 멈춰 둔 러너가 있으면 다시 켭니다.
- 설정, 로그, 사용량 기록은 남습니다. 완전히 지우려면 그다음에 `Remove-Item -Recurse "$env:ProgramData\runner-guard"`를 실행합니다.
- 자동 조정이 바꾼 `.wslconfig`의 `memory=`, `swap=`과 `config.toml`의 `concurrent`는 그대로 남습니다. 바꾸기 전의 `.wslconfig`는 `wslconfig.last-good`에 있습니다.
