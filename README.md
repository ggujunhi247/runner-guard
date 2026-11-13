# runner-guard

GitLab Runner(docker executor) 호스트가 흔한 고장을 스스로 고치고, 못 고치면 잡을 받지 않게 멈추는 단일 바이너리입니다. Linux(systemd)와 Windows(Docker Desktop)를 지원합니다.

## 무엇을 하나

`runner-guard run`이 한 시간마다 아래 순서로 돕니다.

1. **거부 확인**: `refuse_if_exists`에 적은 경로가 하나라도 있으면 아무것도 하지 않고 끝냅니다.
2. **Docker 점검**: `docker info`가 응답하지 않으면 엔진을 재시작합니다(최소 30분 간격, 24시간에 최대 3회).
   - Linux: `systemctl restart docker`
   - Windows: `docker desktop restart`. 이 명령이 없는 버전이면 Docker Desktop을 끝내고, 보조 작업 `runner-guard-desktop`(관리자 권한 아님)으로 다시 띄웁니다.
3. **디스크 점검**: 사용률이 `prune_percent`(75%) 이상이면 러너가 남긴 것을 정리합니다.
   - Linux: Docker 데이터 폴더(`DockerRootDir`)가 있는 파일시스템의 사용률입니다.
   - Docker Desktop: `docker system df` 합계를 `desktop_budget_gb`로 나눈 값과 호스트 드라이브 사용률 중 큰 값입니다. VHDX는 줄어들지 않기 때문입니다.
4. **러너 차단·해제**
   - Docker가 응답하지 않거나 디스크가 `block_percent`(90%) 이상이면, 실행 중인 `gitlab-runner` 서비스를 멈춥니다. 서비스가 멈춰 있으면 잡을 받지 않으니, 실패하는 잡이 쌓이지 않습니다.
   - 정상으로 돌아오고 디스크가 `prune_percent` 미만이 되면 다시 켭니다.
   - 도구가 멈춘 서비스만 도구가 다시 켭니다. 사람이 멈춘 서비스는 건드리지 않습니다.

`runner-guard update`는 하루 한 번 GitHub Release를 확인합니다. 더 높은 버전이고 SHA256이 맞을 때만 자신을 교체합니다. 교체한 새 바이너리가 `selftest`나 `version` 확인을 통과하지 못하면 이전 파일로 되돌립니다.

## 지우는 것과 남기는 것

| 대상 | 기본 | 조건 |
| --- | --- | --- |
| 멈춘 컨테이너 | 지움 | 라벨 `com.gitlab.gitlab-runner.managed=true`가 붙은 것만 |
| 볼륨 | 지움 | 같은 러너 라벨이 붙었고, 어떤 컨테이너도 쓰지 않는 것만 |
| 이미지 | **남김** | `prune_images: true`일 때만, 72시간 전에 만들어졌고 어떤 컨테이너(멈춘 것 포함)도 쓰지 않는 것 |
| 빌드 캐시 | **남김** | `prune_images: true`일 때만, 72시간 전에 만들어진 것 |
| 실행 중인 컨테이너, 러너가 만들지 않은 컨테이너·볼륨 | 남김 | 어떤 설정에서도 지우지 않음 |

`image_age`(72h)는 이미지가 **만들어진 시각** 기준입니다. 마지막으로 쓴 시각이 아닙니다. 이미지 정리는 CI 전용 호스트에서만 켜세요. 운영 서비스가 같이 도는 호스트에서 켜면 롤백용 이미지가 지워질 수 있습니다.

## 설치

[Releases](https://github.com/ggujunhi/runner-guard/releases)에서 OS에 맞는 파일을 받습니다.

**Linux** (root 필요)

```sh
chmod +x runner-guard_linux_amd64
sudo ./runner-guard_linux_amd64 install
sudo runner-guard status
```

`/usr/local/bin/runner-guard`에 복사하고 systemd timer 두 개를 켭니다: `runner-guard-run.timer`(매시간), `runner-guard-update.timer`(매일).

**Windows** (관리자 PowerShell, Docker Desktop을 쓰는 그 사용자 계정으로)

```powershell
.\runner-guard_windows_amd64.exe install
& "$env:ProgramData\runner-guard\runner-guard.exe" status
```

- 필수 조건: **러너 사용자가 늘 로그인해 있어야 합니다(자동 로그인 설정).** Docker Desktop은 그 사용자 세션 안에서만 동작합니다. 그래서 예약 작업도 "사용자가 로그온한 경우에만 실행"으로 등록됩니다.
- `install`은 `%ProgramData%\runner-guard` 폴더 권한을 바꿔서 Administrators와 SYSTEM만 쓸 수 있게 합니다. 일반 사용자가 exe를 바꿔치기해 관리자 권한을 얻는 길을 막기 위해서입니다.
- 작업 스케줄러에 작업 세 개가 생깁니다: `runner-guard-run`(매시간), `runner-guard-update`(매일), `runner-guard-desktop`(재시작용 보조 작업, 직접 실행되지 않음).

> [!WARNING]
> **운영 서비스가 같이 도는 호스트**에서는 첫 실행 전에 `refuse_if_exists`에 그 호스트에만 있는 표식 경로를 넣으세요. 예: `["/etc/<운영-표식-파일>"]`. 그 파일이 있으면 runner-guard는 아무것도 지우거나 멈추지 않습니다.

제거: `runner-guard uninstall`. 예약 작업만 지우고, 설정과 로그는 남깁니다.

## 명령

```
runner-guard check | status [--json]   상태만 보고 아무것도 바꾸지 않음
runner-guard run [--force]             --force: 기준 미만이어도 정리
runner-guard update [--check]          --check: 새 버전이 있는지만 확인
runner-guard install | uninstall
runner-guard selftest | version
```

## 설정

설정 파일: Linux는 `/etc/runner-guard/config.json`, Windows는 `%ProgramData%\runner-guard\config.json`입니다. 파일에 적은 키만 기본값을 덮어씁니다.

| 키 | 기본값 | 뜻 |
| --- | --- | --- |
| `prune_percent` | `75` | 이 사용률 이상이면 정리. 차단 해제 기준이기도 함 |
| `block_percent` | `90` | 정리 뒤에도 이 사용률 이상이면 러너 차단 |
| `desktop_budget_gb` | `60` | Docker Desktop에서 Docker 데이터에 허용할 용량 |
| `image_age` | `"72h"` | 이미지·빌드 캐시 정리 기준(생성 시각) |
| `prune_images` | `false` | 이미지·빌드 캐시도 정리할지 |
| `docker_restart_min_interval` | `"30m"` | Docker 재시작 최소 간격 |
| `docker_restart_max_per_day` | `3` | 24시간 동안의 Docker 재시작 상한 |
| `runner_service` | `"gitlab-runner"` | 멈추고 켤 서비스 이름 |
| `refuse_if_exists` | `[]` | 이 경로 중 하나라도 있으면 `run`이 아무것도 하지 않음 |
| `update_repo` | `"ggujunhi/runner-guard"` | 업데이트를 받을 GitHub 저장소 |
| `auto_update` | `true` | `false`면 `update`가 교체하지 않음(`--check`는 동작) |

상태와 로그 위치: Linux는 `/var/lib/runner-guard/`, Windows는 `%ProgramData%\runner-guard\`입니다(`state.json`, `runner-guard.log`). 로그가 1MB를 넘으면 `runner-guard.log.1`로 한 번 회전합니다. Linux에서는 journald에도 남습니다(`journalctl -u runner-guard-run`).

## 업데이트와 릴리스

```sh
git tag v0.2.0 && git push --tags
```

GitHub Actions가 `runner-guard_linux_amd64`, `runner-guard_windows_amd64.exe`, `checksums.txt`를 Release에 올립니다. 각 호스트는 24시간 안에 스스로 업데이트합니다. 바로 받으려면 호스트에서 `runner-guard update`를 실행하세요.

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
- 알림을 보내지 않습니다. 로그와 `status`로 확인하세요.
- 재부팅 뒤 `gitlab-runner`가 자동으로 켜지는 것은 막지 않습니다. 호스트가 여전히 비정상이면 다음 점검에서 다시 멈춥니다.

## 라이선스

MIT
