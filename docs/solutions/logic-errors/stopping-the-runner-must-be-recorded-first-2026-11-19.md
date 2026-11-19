---
title: Every path that stops the runner service must record it in state first
date: 2026-11-19
category: logic-errors
module: internal/guard (runner stop paths)
problem_type: logic_error
component: service_layer
severity: high
symptoms:
  - The WSL tune path stopped gitlab-runner and relied on a deferred Start to bring it back
  - A process killed mid-tune, a failed Start, or a stop that timed out but finished could leave the runner stopped with nothing to restart it
root_cause: missing_workflow_step
resolution_type: code_fix
related_components:
  - internal/runnersvc
  - internal/state
tags: [gitlab-runner, blocked-by-us, crash-safety, state, wsl, tune]
---

# Every path that stops the runner service must record it in state first

## Problem

runner-guard only ever starts a runner that it stopped itself. It knows it stopped one from `BlockedByUs` in `state.json`. `decide()` in `internal/guard/guard.go` saves `BlockedByUs` before calling `runnersvc.Stop`.

The WSL tuning path added in v0.5.0 (`applyWSL` in `internal/guard/os_windows.go`) stopped the service itself and restarted it with `defer runnersvc.Start(...)`. It never recorded anything. A review found three ways this left a CI host's runner down for good:
- **Killed mid-way:** the process was killed during the up-to-20-minute WSL restart. The `/IT` task dies at logoff, at sleep, or when someone ends it.
- **Start failed:** the error was dropped.
- **Stop timed out:** `Stop` returned an error but the service finished stopping. The code returned without the deferred Start.

In every case `state.json` said nothing about the stop, so later runs treated it as a human's stop and left it alone.

## Root cause

There were two ways to stop the runner. Only one of them followed the rule that makes stopping safe: write the intent durably, then act.

## Solution

`applyWSL` now does what `decide()` does:
1. Before stopping, it sets `BlockedByUs`, `BlockReason = runnersvc.ReasonTune` and `BlockedAt`, then calls `state.Save`. If the save fails, it does not stop the runner.
2. If `Stop` returns an error, it checks the status. It backs out only if the service is still running.
3. It clears the flag only after `Start` succeeds. Otherwise the flag stays.
4. `runnersvc.Decide` lifts a `ReasonTune` block the way it lifts a Docker block: as soon as Docker answers and the disk is below `block_percent`. So the next hourly `run` restarts a runner that a dead or failed tune left stopped.

## Prevention

- **The rule:** any new code that stops the runner must save `BlockedByUs` and a block reason before the stop, and must give that reason a release rule in `runnersvc.Decide`. A deferred restart alone is not enough.
- **Better fix, not done yet:** have one helper that saves state and then stops, so this cannot be done two ways. Whoever owns `internal/guard` should make that change; it means changing product code.
