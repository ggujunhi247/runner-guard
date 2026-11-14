---
title: Reclaiming Docker Desktop disk space on Windows needs a compact, not just a prune
date: 2026-11-14
category: tooling-decisions
module: internal/disk (Windows reclaim)
problem_type: tooling_decision
component: infrastructure
severity: high
applies_when:
  - A Windows runner uses Docker Desktop on WSL2
  - Disk usage stays high after docker prune
  - Changing how runner-guard measures or frees Docker Desktop disk space
retire_when: Docker Desktop ships automatic, supported shrinking of docker_data.vhdx; check its release notes for sparse or auto-compact of the data disk
tags: [docker-desktop, wsl2, vhdx, diskpart, fstrim, windows, disk-space]
---

# Reclaiming Docker Desktop disk space on Windows needs a compact, not just a prune

## Context

On a Windows host, Docker Desktop keeps its data on a single disk image, `docker_data.vhdx`, attached to the `docker-desktop` WSL distro.
- **The file never shrinks by itself.** Space freed by `docker prune` is freed only inside the image.
- **Measured on one Docker Desktop 4.86 host:** `docker system df` reported about 200GB, but the VHDX file was 302GB and not sparse.

Before the fix, runner-guard's Windows usage figure took the host drive's used percent, and that number did not move after a prune. A runner stopped for disk could therefore stay stopped for good.

Two more measured facts shaped the design:
- **`docker system df` takes minutes.** It took about 4 minutes on that host with 149 volumes, so it needs the prune timeout, not 60s.
- **The VHDX location is configurable.** Docker Desktop's `CustomWslDistroDir` setting (in `%APPDATA%\Docker\settings-store.json`) can move it, so the image may not be on the system drive. The default location is `%LOCALAPPDATA%\Docker\wsl\disk\docker_data.vhdx`, and a moved image sits at `<CustomWslDistroDir>\disk\docker_data.vhdx`.

## Guidance

Use the procedure in `internal/disk/reclaim_windows.go`.

1. Run `wsl -d docker-desktop -u root fstrim -a`. This marks free blocks; a sparse VHDX hands them back to the drive at this point, and the procedure stops there.
2. Check the gain first. If the VHDX file size minus `docker system df` is under 10GB (`reclaimMinGain`), stop without touching Docker.
3. Stop Docker Desktop with `docker desktop stop`, falling back to `taskkill`. Then run `wsl --shutdown`.
4. Compact with diskpart. Run `diskpart /s <script>` using:
   `select vdisk file=…`, `attach vdisk readonly`, `compact vdisk`, `detach vdisk`.
   If the compact fails, still run `detach vdisk`.
5. Relaunch Docker Desktop through the non-admin `runner-guard-desktop` task, never from the elevated process.

Run this only while the runner is stopped for disk, at most once per `ReclaimEvery` (24h), because Docker is down throughout.

Measure Windows usage on the drive that holds the VHDX (`filepath.VolumeName` of its path), not on `%SystemDrive%`.

**Rejected alternatives:**
- **`Optimize-VHD`:** it needs the Hyper-V PowerShell module, which a Docker Desktop host on WSL2 may not have. diskpart is always present.
- **Making the disk sparse with `wsl --manage docker-desktop --set-sparse true`:** community reports say WSL 2.5.6+ requires `--allow-unsafe` for it, and some report sparse disks that stop reclaiming or get corrupted. A sparse VHDX also refuses diskpart compact, so the code detects sparse and relies on fstrim alone.
- **Docker's own tooling:** none found that does this automatically as of Docker Desktop 4.86.

## Why This Matters

Without the compact, usage on Docker Desktop only rises. Blocking the runner at 90% then becomes a permanent outage instead of a pause.

Compacting while a job runs would kill the job. That is why reclaim is tied to the runner already being stopped for disk.

## When to Apply

- Changing anything in `internal/disk/usage_windows.go` or `reclaim_windows.go`.
- Diagnosing a Windows runner that stays blocked even though `docker system df` looks small.

## Examples

`runner-guard status` on that host before the change: `docker 199.7GB of 60GB budget, C: 90.0% used`. It measured C:, while the VHDX lived on another drive.

## Related

- Pending verification: as of v0.2.0 the compact path has been tested only with a fake command runner, never run against a real VHDX.
- Sources: docker/desktop-feedback issue 412 (VHDX growth that prune does not reclaim); community guides on prune + fstrim + diskpart compact.
