# runner-guard

GitLab Runner(docker executor) 호스트가 흔한 고장을 스스로 고치고, 못 고치면 잡을 받지 않게 멈추는 단일 바이너리입니다. Linux(systemd)와 Windows(Docker Desktop)를 지원합니다.

## 무엇을 하나

`runner-guard run`이 한 시간마다 아래 순서로 돕니다.

1. **거부 확인**: `refuse_if_exists`에 적은 경로가 하나라도 있으면 아무것도 하지 않고 끝냅니다.
2. **Docker 점검**: `docker info`가 10초 간격으로 세 번 연속 응답하지 않으면 엔진을 재시작합니다(최소 30분 간격, 24시간에 최대 3회). 재시작은 돌던 잡을 모두 죽이므로, 바쁜 엔진이 한 번 늦게 답한 것만으로는 재시작하지 않습니다.
   - Linux: `systemctl restart docker`
   - Windows: `docker desktop restart`. 이 명령이 없는 버전이면 Docker Desktop을 끝내고, 보조 작업 `runner-guard-desktop`(관리자 권한 아님)으로 다시 띄웁니다.
   - 하루 재시작 횟수를 다 쓰고도 Docker가 죽어 있으면:
     1. Windows는 `wsl --shutdown` 뒤 Docker Desktop을 다시 띄웁니다(하루 한 번).
     2. 그래도 안 되면 `reboot_recover`가 켜져 있을 때만 2분 뒤 재부팅합니다. 조건은 7일에 한 번, Windows는 자동 로그온(`AutoAdminLogon=1`)이 켜져 있을 때뿐입니다.
     3. 재부팅 뒤에도 Docker가 죽어 있으면 **다시 재부팅하지 않고** 사람을 부릅니다. Docker가 살아나면 "돌아왔다" 알림을 보냅니다.
3. **디스크 점검**: 사용률이 `prune_percent`(75%) 이상이면 러너가 남긴 것을 정리합니다.
   - Linux: Docker 데이터 폴더(`DockerRootDir`)가 있는 파일시스템의 사용률입니다.
   - Docker Desktop: `docker system df` 합계를 `desktop_budget_gb`로 나눈 값과, Docker 데이터 디스크(VHDX)가 있는 드라이브의 사용률 중 큰 값입니다.
4. **러너 차단·해제**
   - Docker가 응답하지 않거나 디스크가 `block_percent`(90%) 이상이면, 실행 중인 `gitlab-runner` 서비스를 멈춥니다. 서비스가 멈춰 있으면 잡을 받지 않으니, 실패하는 잡이 쌓이지 않습니다.
   - 다시 켜는 기준은 멈춘 이유에 따라 다릅니다.
     - 디스크 때문에 멈췄으면, 사용률이 `prune_percent` 미만이 될 때 켭니다. 90% 근처에서 껐다 켜기를 반복하지 않게 하기 위해서입니다.
     - Docker 때문에 멈췄으면, Docker가 다시 응답하고 디스크가 `block_percent` 미만이면 바로 켭니다.
   - 도구가 멈춘 서비스만 도구가 다시 켭니다. 사람이 멈춘 서비스는 건드리지 않습니다.
5. **Windows 디스크 공간 회수**: Docker Desktop의 데이터 디스크(VHDX)는 안에서 지워도 줄어들지 않습니다. 그래서 디스크 때문에 러너를 멈춘 상태(= 잡이 돌고 있지 않음)에서만, 하루 한 번까지 아래 순서로 공간을 드라이브에 돌려줍니다.
   1. `fstrim`
   2. Docker Desktop 종료, `wsl --shutdown`
   3. `diskpart`로 VHDX 압축
   4. Docker Desktop을 사용자 권한으로 다시 실행
   5. 다시 재고, 기준 아래로 내려갔으면 러너를 바로 켭니다.
   - 줄일 수 있는 양이 10GB 미만이면 Docker를 멈추지 않고 건너뜁니다.
   - sparse VHDX면 `fstrim`만으로 공간이 돌아오므로 압축하지 않습니다.

`runner-guard update`는 하루 한 번 GitHub Release를 확인합니다. 더 높은 버전이고 SHA256이 맞을 때만 자신을 교체합니다. 교체한 새 바이너리가 `selftest`나 `version` 확인을 통과하지 못하면 이전 파일로 되돌립니다.

### 병목: 손대는 것과 알리기만 하는 것

runner-guard는 두 조건을 모두 만족할 때만 직접 조치합니다. 러너가 남긴 것이 확실해야 하고, 정상적으로 도는 잡을 해칠 수 없어야 합니다. 어느 하나라도 아니면 측정하고 알리기만 합니다.

| 병목 | 동작 |
| --- | --- |
| 러너 라벨이 붙은 컨테이너가 `stuck_container_age`보다 오래 실행 중 | **강제 삭제**. 기본으로 켜져 있고 기준은 `"6h"`입니다. GitLab 기본 잡 시간 제한(1시간)보다 넉넉한 값입니다. 6시간보다 오래 도는 잡이 있으면 그보다 큰 값으로 올리고(최소 `"1h"`), `""`로 두면 꺼집니다. 그 시간을 넘긴 잡은 GitLab에서 이미 끝난 잡이라, 컨테이너는 CPU·메모리·디스크만 차지합니다 |
| `docker info` 응답이 20초 이상 | 기록, 3번 연속이면 알림 |
| 메모리 사용률 90% 이상 | 기록, 3번 연속이면 알림 |
| 부하가 CPU당 2.0 이상 (Linux만) | 기록, 3번 연속이면 알림 |
| 동시 잡 수, WSL 메모리·스왑, 정리 시작 기준 | 바로 손대지 않음. 데이터가 충분히 쌓인 뒤 [자동 조정](#자동-조정)이 하루 한 단계씩만 바꿉니다 |
| 느린 잡 | 손대지 않음. 무거운 잡과 아픈 호스트를 도구가 구분할 수 없습니다 |

`run`은 한 시간마다 돌기 때문에, "3번 연속"은 약 3시간 동안 계속됐다는 뜻입니다. 알림은 그 시점에 한 번만 보내고, 값이 정상으로 돌아오면 횟수를 0부터 다시 셉니다.

`alert_webhook`을 설정하면 아래 경우에 메시지를 보냅니다. 정리(prune)만 한 경우에는 보내지 않습니다.
- 러너를 멈추거나 켤 때
- Docker를 재시작할 때(실패 포함)
- 디스크를 회수할 때
- 오래 멈춘 컨테이너를 지울 때
- 부하가 계속될 때
- 자동 조정이 값을 바꿀 때
- `run`이 실패할 때

새 일이 없어도 러너가 6시간 넘게 멈춰 있거나 Docker가 계속 죽어 있으면, 6시간마다 다시 알립니다. 웹훅 오류 메시지에는 URL을 넣지 않습니다.

## 자동 조정

`runner-guard sample`이 5분마다 사용량을 `history.jsonl`에 남깁니다(30일 보관).
- 남기는 것:
  - 돌고 있는 잡 수
  - `concurrent`
  - Docker가 쓰는 메모리. Linux는 호스트, Windows는 WSL VM 안의 `/proc/meminfo`입니다.
  - 스왑 인(`pswpin`)과 OOM kill 횟수
  - 잡별 메모리와 다른 컨테이너 메모리
- 판단에서 빼는 표본: 러너가 멈춘 동안, Docker가 죽은 동안, 회수·재시작 뒤 1시간, 업데이트 뒤 48시간. 이때의 "잡 0개"는 여유가 아니기 때문입니다.

`run`이 하루 한 번 이 기록을 보고 **많아야 하나만** 바꿉니다.

| 대상 | 올리는 조건 | 내리는 조건 | 범위 |
| --- | --- | --- | --- |
| gitlab-runner `concurrent` | 처음 값에서 14일·잡 20시간 이상의 기록이 있고, 바쁜 표본의 25% 이상이 상한에 닿음. 바꾼 뒤에는 3일 더 쌓여야 다음에 올림 | 최근 24시간 잡이 돌 때 OOM kill, 큰 스왑 인, 메모리 90% 이상이 보이면 바로 1 내림. 메모리에 들어가는 잡 수보다 크거나 CPU 코어 수보다 크면 내림 | 1 ~ min(코어 수, (메모리 − 다른 컨테이너 p99 − 2GB) ÷ 잡 메모리 p95) |
| WSL `memory` (Windows) | VM에 메모리 부족이 보이고, 호스트 메모리 p95가 80% 미만 | VM이 그만큼 쓴 적이 없고, 호스트 메모리 p95가 85% 이상 | 2GB 단위, 주 1회, 4GB ~ 호스트 RAM − 4GB |
| WSL `swap` (Windows) | 스왑이 거의 찬 채로 OOM kill | 내리지 않음 | 2GB 단위, 16GB까지 |
| `prune_percent` | 올리지 않음 | 가장 빠른 디스크 증가 속도로도 차단 전에 정리가 3번 돌 여유가 없을 때 2씩 | 60 ~ 설정값 |

- **되돌리기**: 올린 뒤 72시간 안에 문제가 보이면 이전 값으로 되돌리고, 실패한 값을 상한으로 기억합니다. 두 번 되돌리면 그 항목은 멈추고 알립니다.
- **적용 방법**:
  - `concurrent`: `config.toml`의 맨 위 `concurrent = N` 한 줄만 바꿉니다. gitlab-runner가 1분 안에 스스로 다시 읽습니다. 파일에는 러너 토큰이 있어서 복사본을 만들지 않습니다.
  - WSL 값:
    1. 잡이 없는지 1분 간격으로 두 번 확인합니다.
    2. 러너를 멈추고 한 번 더 확인합니다.
    3. 사용자 `.wslconfig`를 고칩니다.
    4. `wsl --shutdown` 뒤 Docker Desktop을 다시 띄웁니다. 재부팅은 필요 없습니다.
    5. 10분 안에 Docker가 돌아오지 않거나 VM 메모리가 목표와 10% 넘게 다르면, 이전 파일로 되돌리고 그 항목을 멈춥니다.
    - 디스크 회수와 합쳐 하루 한 번까지입니다.
- **하지 않는 것**:
  - Linux 스왑은 바꾸지 않고 기록에 권고만 남깁니다.
  - `block_percent`는 안전선이라 배우지 않습니다.
  - 설정은 `0 < prune_percent < block_percent ≤ 95`여야 합니다. 어긋나면 `run`은 멈추지 않고, 값을 보정해서 쓰면서 경고를 남깁니다.
  - WSL 값을 바꾸려고 러너를 멈출 때는 멈추기 전에 상태에 기록합니다. 도중에 프로세스가 죽어도 다음 `run`이 러너를 다시 켭니다. UTF-16으로 저장된 `.wslconfig`는 고치지 않고 알립니다.
- **끄기**: 설정에서 `auto_tune: false`를 두거나 상태 폴더에 `tune.disabled` 파일을 만들면, 바꾸지 않고 "이렇게 바꾸겠다"는 알림만 보냅니다(내용이 바뀔 때만).
- `runner-guard tune`은 지금 무엇을 바꿀지 보여 주기만 합니다. `runner-guard tune --reset`은 배운 값, 상한, 멈춤을 모두 지웁니다.

## 지우는 것과 남기는 것

| 대상 | Linux 기본 | Windows 기본 | 조건 |
| --- | --- | --- | --- |
| 멈춘 컨테이너 | 지움 | 지움 | 라벨 `com.gitlab.gitlab-runner.managed=true`가 붙은 것만 |
| 볼륨 | 지움 | 지움 | 같은 러너 라벨이 붙었고, 어떤 컨테이너도 쓰지 않는 것만 |
| 이미지 | **남김** | 지움 | `prune_images: true`일 때만. 72시간 전에 만들어졌고 어떤 컨테이너(멈춘 것 포함)도 쓰지 않는 것 |
| 빌드 캐시 | **남김** | 지움 | `prune_images: true`일 때만. 72시간 전에 만들어진 것 |
| 실행 중인 컨테이너, 러너가 만들지 않은 컨테이너·볼륨 | 남김 | 남김 | 어떤 설정에서도 지우지 않음 |

`image_age`(72h)는 이미지가 **만들어진 시각** 기준입니다. 마지막으로 쓴 시각이 아닙니다.

Windows 러너는 CI 전용 호스트로 보고, 이미지 정리와 디스크 회수를 기본으로 켭니다. Linux는 운영 서비스가 같이 도는 호스트일 수 있어서 이미지를 남깁니다. 지우면 롤백용 이미지가 사라질 수 있기 때문입니다. CI 전용 Linux 호스트라면 설정에서 `prune_images: true`로 켜세요.

## 설치

[Releases](https://github.com/ggujunhi247/runner-guard/releases)에서 OS에 맞는 파일을 받습니다.

**Linux** (root 필요)

```sh
chmod +x runner-guard_linux_amd64
sudo ./runner-guard_linux_amd64 install
sudo runner-guard status
```

`/usr/local/bin/runner-guard`에 복사하고 systemd timer 세 개를 켭니다: `runner-guard-run.timer`(매시간), `runner-guard-update.timer`(매일), `runner-guard-sample.timer`(5분마다). 첫 실행은 설치 15분 뒤입니다. 이전 버전에서 업데이트한 호스트는 다음 `run`이 sample timer를 추가합니다.

**Windows** (관리자 PowerShell)

```powershell
.\runner-guard_windows_amd64.exe install
& "$env:ProgramData\runner-guard\runner-guard.exe" status
```

- 필수 조건: **러너 사용자가 늘 로그인해 있어야 합니다(자동 로그인 설정).** Docker Desktop은 그 사용자 세션 안에서만 동작합니다. 그래서 예약 작업도 "사용자가 로그온한 경우에만 실행"으로 등록됩니다.
- **Docker Desktop을 쓰는 그 사용자 계정으로 관리자 PowerShell을 여세요.** 다른 관리자 계정으로 권한을 올려서 설치하면, 작업이 로그인하지 않는 그 관리자 계정에 등록되어 돌지 않습니다. 설치 뒤 `schtasks /Query /TN runner-guard-run /V /FO LIST`에서 "Run As User"를 확인하세요.
- `install`은 `%ProgramData%\runner-guard` 폴더 권한을 바꿔서 Administrators와 SYSTEM만 쓸 수 있게 합니다. 일반 사용자가 exe를 바꿔치기해 관리자 권한을 얻는 길을 막기 위해서입니다.
- 작업 스케줄러에 작업 네 개가 생깁니다.
  - `runner-guard-run`: 매시간, 첫 실행은 설치 15분 뒤
  - `runner-guard-update`: 매일, run보다 30분 늦게 돌아서 서로 잠금을 다투지 않음
  - `runner-guard-sample`: 5분마다, `conhost --headless`로 돌아서 창이 뜨지 않음. 이전 버전에서 업데이트한 호스트는 다음 `run`이 추가합니다.
  - `runner-guard-desktop`: Docker Desktop을 다시 띄우는 보조 작업, 직접 실행되지 않음
- Docker 데이터 디스크 위치는 Docker Desktop 설정(`CustomWslDistroDir`)에서 자동으로 찾습니다. 못 찾으면 `desktop_vhdx`에 경로를 적으세요.
- 설치 직후 `status`로 디스크 사용률을 보세요. 러너가 지울 수 없는 데이터(다른 볼륨 등)만으로 `block_percent`를 넘는다면, `desktop_budget_gb`를 늘리지 않는 한 러너가 계속 멈춰 있게 됩니다.

> [!WARNING]
> **운영 서비스가 같이 도는 호스트**에서는 첫 실행(설치 15분 뒤) 전에 `refuse_if_exists`에 그 호스트에만 있는 표식 경로를 넣으세요. 예: `["/etc/<운영-표식-파일>"]`. 그 파일이 있으면 runner-guard는 아무것도 지우거나 멈추지 않습니다.

제거: `runner-guard uninstall`. 예약 작업을 지우고, 도구가 멈춰 둔 러너가 있으면 다시 켭니다. 설정과 로그는 남깁니다.

## 명령

```
runner-guard check | status [--json]   상태만 보고 아무것도 바꾸지 않음
runner-guard run [--force]             --force: 기준 미만이어도 정리
runner-guard update [--check]          --check: 새 버전이 있는지만 확인
runner-guard sample                    사용량 표본 하나 기록(5분마다 예약됨)
runner-guard tune [--reset]            자동 조정이 무엇을 바꿀지 보기 / 배운 값 지우기
runner-guard install | uninstall
runner-guard selftest | version
```

## 설정

설정 파일: Linux는 `/etc/runner-guard/config.json`, Windows는 `%ProgramData%\runner-guard\config.json`입니다. 파일에 적은 키만 기본값을 덮어씁니다.

| 키 | 기본값 | 뜻 |
| --- | --- | --- |
| `prune_percent` | `75` | 이 사용률 이상이면 정리. 디스크 차단 해제 기준이기도 함 |
| `block_percent` | `90` | 정리 뒤에도 이 사용률 이상이면 러너 차단 |
| `desktop_budget_gb` | `60` | Docker Desktop에서 Docker 데이터에 허용할 용량 |
| `image_age` | `"72h"` | 이미지·빌드 캐시 정리 기준(생성 시각) |
| `prune_images` | Linux `false`, Windows `true` | 이미지·빌드 캐시도 정리할지 |
| `desktop_reclaim` | Linux `false`, Windows `true` | 디스크 차단 중에 Docker Desktop VHDX 공간을 회수할지 |
| `desktop_vhdx` | `""`(자동) | Docker Desktop 데이터 디스크 경로 |
| `docker_restart_min_interval` | `"30m"` | Docker 재시작 최소 간격 |
| `docker_restart_max_per_day` | `3` | 24시간 동안의 Docker 재시작 상한 |
| `runner_service` | `"gitlab-runner"` | 멈추고 켤 서비스 이름 |
| `refuse_if_exists` | `[]` | 이 경로 중 하나라도 있으면 `run`이 아무것도 하지 않음 |
| `stuck_container_age` | `"6h"` | 이보다 오래 실행 중인 러너 컨테이너를 삭제. 최소 `"1h"`, 가장 긴 잡 시간 제한보다 크게. `""`면 끔 |
| `alert_webhook` | `""`(끔) | 알림을 보낼 웹훅 URL. Slack incoming webhook, Discord(URL 끝에 `/slack`) 등 |
| `update_repo` | `"ggujunhi247/runner-guard"` | 업데이트를 받을 GitHub 저장소 |
| `auto_update` | `true` | `false`면 `update`가 교체하지 않음(`--check`는 동작) |
| `auto_tune` | `true` | `false`면 자동 조정이 바꾸지 않고 제안만 알림 |
| `runner_config` | Linux `"/etc/gitlab-runner/config.toml"`, Windows `"C:\\GitLab-Runner\\config.toml"` | `concurrent`를 읽고 바꿀 gitlab-runner 설정 파일 |
| `reboot_recover` | Linux `false`, Windows `true` | Docker가 재시작으로도 살아나지 않을 때 재부팅으로 복구할지. 운영 컨테이너가 있는 Linux 호스트는 끈 채로 두세요 |

웹훅 URL은 비밀입니다. 호스트의 설정 파일에만 두고 저장소에 올리지 마세요.

상태와 로그 위치: Linux는 `/var/lib/runner-guard/`, Windows는 `%ProgramData%\runner-guard\`입니다(`state.json`, `runner-guard.log`, `history.jsonl`). 로그가 1MB를 넘으면 `runner-guard.log.1`로 한 번 회전합니다. Linux에서는 journald에도 남습니다(`journalctl -u runner-guard-run`).

## 업데이트와 릴리스

```sh
git tag v0.3.0 && git push origin v0.3.0
```

GitHub Actions가 순서대로 처리합니다.

1. 태그가 `vX.Y.Z` 형식인지 확인합니다. 다른 형식은 거부합니다.
2. 두 OS용 바이너리를 만들고, 각 OS에서 `selftest`와 `version`을 확인합니다.
3. `runner-guard_linux_amd64`, `runner-guard_windows_amd64.exe`, `checksums.txt`를 Release에 올립니다.
4. **바로 이전 Release의 바이너리로 실제 `update`를 실행해서** 새 버전으로 바뀌는지 Linux·Windows 모두에서 확인합니다(`update-smoke`).

각 호스트는 24시간 안에 스스로 업데이트합니다. 바로 받으려면 호스트에서 `runner-guard update`를 실행하세요.

- 버전은 semver가 올라갈 때만 바뀌고, 낮아지지 않습니다.
- GitHub가 403·429(요청 한도)로 답하거나 Release에 이 OS용 파일이 없으면, 그날 업데이트는 건너뜁니다.

## Windows Defender / Smart App Control

exe에 서명하지 않았습니다. Defender나 Smart App Control이 막으면 우회 도구를 쓰지 말고, 받은 파일의 SHA256이 `checksums.txt`와 같은지 확인한 뒤 예외로 등록하세요.

```powershell
Get-FileHash .\runner-guard_windows_amd64.exe -Algorithm SHA256
Add-MpPreference -ExclusionPath "$env:ProgramData\runner-guard"
```

Smart App Control이 켜진 PC에서는 서명하지 않은 exe를 예외로 둘 수 없습니다. 러너 호스트에서는 Smart App Control을 끄는 것이 전제입니다.

## 한계

- `checksums.txt`는 같은 Release에서 받습니다. 전송 중 손상은 막지만, Release를 올리는 GitHub 계정이 탈취되면 막지 못합니다. 서명 검증(공개키 내장)은 v2 후보입니다.
- 재부팅 뒤 `gitlab-runner`가 자동으로 켜지는 것은 막지 않습니다. 호스트가 여전히 비정상이면 다음 점검에서 다시 멈춥니다.
- Windows 디스크 회수는 Docker를 수 분에서 수십 분 멈춥니다. 그래서 러너를 이미 멈춘 상태에서만, 하루 한 번까지 합니다.

## 라이선스

MIT
