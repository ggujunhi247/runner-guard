---
title: A tuned knob's current value comes from the file it edits, and what is recorded is exactly what was written
date: 2026-11-20
category: logic-errors
module: internal/tune, internal/guard (auto-tune)
problem_type: logic_error
component: service_layer
severity: medium
symptoms:
  - "WSL memory read from the VM's MemTotal (about 2% below .wslconfig memory=) so 32GB counted as 31 and every step drifted to odd values"
  - "concurrent came from the newest history line, which is usually a run record without Concurrent, so hand edits to config.toml went unseen"
  - "Recording a fractional target (9.81) while writing an integer (9GB) made the next run treat our own change as a hand edit and wipe the revert data"
root_cause: logic_error
resolution_type: code_fix
related_components:
  - internal/state
tags: [tune, wslconfig, gitlab-runner, concurrent, hand-edit, resync]
---

# A tuned knob's current value comes from the file it edits

## Problem

The auto-tuner took each knob's "current value" from a measured stand-in instead of the setting it changes:
- **WSL memory:** taken from `MemTotal` inside the VM. The kernel reserves about 2%: `memory=20GB` reads 20481664 kB, which is 19.5 GiB. At 32GB the rounding gives 31, so raises wrote 33, 35 and so on.
- **`concurrent`:** taken from the last history line. `Run` appends a `kind:"run"` record without `Concurrent` just before the daily tune, so the hand-edit check (`SyncKnob`) was usually skipped. A tuner could then raise past an admin's lower value, or fail with "changed by hand?" and freeze the knob.

The fix added a resync against the file. That created a second trap: a value recorded in state that differs from what was written looks like a hand edit, which resets the knob and loses its revert and ceiling data.

## Root cause

The knob's truth lives in the file (`config.toml`, `.wslconfig`). Measurements of its effect are not the setting.

## Solution

- `guard.TunePlan` reads `tune.ReadConcurrent(RunnerConfig)` and, on Windows, `tune.WSLValue(.wslconfig, "memory"|"swap")`. It passes them in `tune.Input{Concurrent, WSLMemGB, WSLSwapGB}`, and runs `SyncKnob` against them.
- `decideWSL` rounds the file value to whole GB, because `applyChange` writes `int(c.To)` GB. The value `Record` keeps then equals what is in the file.
- `TestOwnWSLChangeIsNotAHandEdit` (`internal/tune/tune_test.go`) checks this from an `8000MB` setting. `TestTunePlanReadsConfigAndSeesHandEdits` (`internal/guard/guard_test.go`) checks that real hand edits are seen.

## Prevention

A new tunable knob reads its current value from the file it edits, never from a sample or measurement. Its `Change.To` is the exact value written (same unit and rounding), so `Record` and the next `SyncKnob` agree.
