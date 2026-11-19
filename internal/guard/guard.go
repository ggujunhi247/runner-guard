// Package guard runs one check-and-repair pass over the runner host.
package guard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/disk"
	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/pressure"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/state"
	"github.com/ggujunhi247/runner-guard/internal/tune"
)

// LockStale is how old a lock must be before another run takes it over.
const LockStale = 2 * time.Hour

// After a restart or compact, wait up to restartWaits*restartPoll for the engine.
const (
	restartWaits = 18
	restartPoll  = 10 * time.Second
)

// ReclaimEvery limits Docker Desktop disk compaction (it stops Docker).
const ReclaimEvery = 24 * time.Hour

// RemindEvery repeats the alert while the runner stays stopped or Docker stays dead.
const RemindEvery = 6 * time.Hour

// RebootEvery limits recovery reboots.
const RebootEvery = 7 * 24 * time.Hour

type Deps struct {
	R                   execx.Runner
	Cfg                 config.Config
	StatePath, LockPath string
	Now                 func() time.Time
	Log                 io.Writer
	Exists              func(string) bool
	// Usage overrides disk.Usage (tests). Nil uses disk.Usage.
	Usage func(context.Context) (float64, string, error)
	// Reclaim overrides disk.Reclaim (tests). Nil uses disk.Reclaim.
	Reclaim func(context.Context) (string, error)
	// Sleep waits between health polls. Nil means no wait.
	Sleep func(time.Duration)
	// Alert receives one message per run in which something changed. Nil: no alerts.
	Alert func(ctx context.Context, msg string) error
	// Pressure overrides pressure.Read (tests). Nil uses pressure.Read.
	Pressure func() (pressure.Sample, error)
	// HistoryPath is history.jsonl. Empty turns off the run record and tuning.
	HistoryPath string
	// Apply overrides how a tune change is applied (tests). Nil: the real thing.
	Apply func(context.Context, tune.Change) error
}

func (d Deps) sleep(t time.Duration) {
	if d.Sleep != nil {
		d.Sleep(t)
	}
}

// MeasurePressure samples host memory and load.
func (d Deps) MeasurePressure() (pressure.Sample, error) {
	if d.Pressure != nil {
		return d.Pressure()
	}
	return pressure.Read()
}

type Report struct {
	DockerHealthy bool
	Restarted     bool
	Pct           float64
	Pruned        bool
	Reclaimed     string
	Action        runnersvc.Action
	Warnings      []string
	DockerLatency time.Duration
	Pressure      pressure.Sample
	Stuck         []string // runner containers removed for running past stuck_container_age
}

// MeasureUsage returns the disk used percent for this host.
func (d Deps) MeasureUsage(ctx context.Context) (float64, string, error) {
	if d.Usage != nil {
		return d.Usage(ctx)
	}
	return disk.Usage(ctx, d.R, d.Cfg)
}

func (d Deps) reclaim(ctx context.Context) (string, error) {
	if d.Reclaim != nil {
		return d.Reclaim(ctx)
	}
	relaunch := func() error {
		_, err := d.R.Run(ctx, time.Minute, "schtasks", "/Run", "/TN", dockerctl.DesktopTask)
		return err
	}
	return disk.Reclaim(ctx, d.R, d.Cfg, relaunch)
}

func (d Deps) exists(p string) bool {
	if d.Exists != nil {
		return d.Exists(p)
	}
	_, err := os.Stat(p)
	return err == nil
}

// stillBlocked appends a note to early-exit errors so nobody forgets the
// runner is down because of us.
func (d Deps) stillBlocked(err error) error {
	if st, _ := state.Load(d.StatePath); st.BlockedByUs {
		return fmt.Errorf("%w (note: %s is still stopped by runner-guard)", err, d.Cfg.RunnerService)
	}
	return err
}

// Run: refuse → lock → state → Docker → disk → runner service → save.
func Run(ctx context.Context, d Deps, force bool) (Report, error) {
	var rep Report
	for _, p := range d.Cfg.RefuseIfExists {
		if d.exists(p) {
			return rep, d.stillBlocked(fmt.Errorf("refusing to run: %s exists (refuse_if_exists)", p))
		}
	}
	_, restartMin, err := d.Cfg.Durations()
	if err != nil {
		return rep, d.stillBlocked(err)
	}
	unlock, err := state.Lock(d.LockPath, LockStale)
	if err != nil {
		return rep, err
	}
	defer unlock()

	now := d.Now()
	logf := func(format string, a ...any) { Logf(d.Log, now, format, a...) }
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		rep.Warnings = append(rep.Warnings, msg)
		logf("warning: %s", msg)
	}
	st, w := state.Load(d.StatePath)
	if w != "" {
		warn("%s", w)
	}
	if w := d.Cfg.Clamp(); w != "" {
		warn("%s", w)
	}
	// After a recovery reboot Docker gets a fresh set of restarts: Docker
	// Desktop may simply not have been started at logon.
	if st.RebootPending {
		var kept []time.Time
		for _, t := range st.DockerRestarts {
			if t.After(st.LastReboot) {
				kept = append(kept, t)
			}
		}
		st.DockerRestarts = kept
	}
	var did []string
	waitHealthy := func() {
		for i := 0; i < restartWaits && !rep.DockerHealthy; i++ {
			d.sleep(restartPoll)
			rep.DockerHealthy = dockerctl.Healthy(ctx, d.R)
		}
	}

	// Docker first: without an engine, pruning is pointless.
	start := time.Now()
	err = dockerctl.Check(ctx, d.R)
	rep.DockerLatency = time.Since(start)
	// A restart kills every running job: one slow answer from a busy engine
	// is not enough, ask twice more.
	for i := 0; i < 2 && err != nil && !errors.Is(err, execx.ErrNotFound); i++ {
		d.sleep(restartPoll)
		err = dockerctl.Check(ctx, d.R)
	}
	if errors.Is(err, execx.ErrNotFound) {
		// A misconfigured guard must not take the runner down.
		return rep, d.stillBlocked(fmt.Errorf("docker CLI not found in PATH; nothing changed: %w", err))
	}
	rep.DockerHealthy = err == nil
	if !rep.DockerHealthy {
		logf("docker unhealthy: %v", err)
		if dockerctl.CanRestart(now, st.DockerRestarts, restartMin, d.Cfg.DockerRestartMaxPerDay) {
			st.DockerRestarts = append(st.DockerRestarts, now)
			rep.Restarted = true
			if err := dockerctl.Restart(ctx, d.R); err != nil {
				warn("docker restart failed: %v", err)
				did = append(did, fmt.Sprintf("docker restart FAILED: %v", err))
			} else {
				waitHealthy()
				did = append(did, fmt.Sprintf("restarted docker (healthy after: %v)", rep.DockerHealthy))
			}
		} else {
			warn("docker unhealthy and restart not allowed (interval or daily cap)")
			if len(dockerctl.Trim(now, st.DockerRestarts)) >= d.Cfg.DockerRestartMaxPerDay {
				did = append(did, d.recoverDocker(ctx, &st, now, &rep, waitHealthy, warn)...)
			}
		}
	}
	if st.RebootPending && rep.DockerHealthy {
		st.RebootPending = false
		did = append(did, "Docker is back after the recovery reboot")
	}

	measured, firstPct := false, 0.0
	measure := func(label string) {
		pct, desc, err := d.MeasureUsage(ctx)
		if err != nil {
			warn("disk usage%s: %v", label, err)
			measured = false
			return
		}
		measured, rep.Pct = true, pct
		logf("disk%s %s", label, desc)
	}
	stuckAge, _ := d.Cfg.StuckAge() // validated by Durations above
	if rep.DockerHealthy && stuckAge > 0 {
		removed, err := disk.RemoveStuck(ctx, d.R, now, stuckAge)
		if err != nil {
			warn("stuck containers: %v", err)
		}
		if len(removed) > 0 {
			rep.Stuck = removed
			did = append(did, fmt.Sprintf("removed %d runner container(s) running longer than %s", len(removed), stuckAge))
		}
	}

	if rep.DockerHealthy {
		measure("")
		firstPct = rep.Pct
		if measured && disk.ShouldPrune(rep.Pct, PrunePercent(d, st), force) {
			rep.Pruned = true
			did = append(did, fmt.Sprintf("pruned at %.1f%%", rep.Pct))
			if err := disk.Prune(ctx, d.R, d.Cfg); err != nil {
				warn("prune: %v", err)
			}
			measure(" after prune")
		}
	}

	name := d.Cfg.RunnerService
	running, exists, err := runnersvc.Status(ctx, d.R, name)
	decide := func() {
		rep.Action = runnersvc.Decide(rep.DockerHealthy, rep.Pct, d.Cfg.PrunePercent, d.Cfg.BlockPercent, running, st.BlockedByUs, st.BlockReason)
		switch rep.Action {
		case runnersvc.Block:
			reason := runnersvc.ReasonDisk
			if !rep.DockerHealthy {
				reason = runnersvc.ReasonDocker
			}
			if err := d.stopRunner(ctx, &st, reason, now); err != nil {
				warn("%v", err)
				return
			}
			running = false
			did = append(did, fmt.Sprintf("stopped %s (%s: docker healthy=%v, disk %.1f%%)", name, reason, rep.DockerHealthy, rep.Pct))
		case runnersvc.Unblock:
			if err := runnersvc.Start(ctx, d.R, name); err != nil {
				warn("start %s: %v", name, err)
				return
			}
			running, st.BlockedByUs, st.BlockReason = true, false, ""
			did = append(did, fmt.Sprintf("started %s (disk %.1f%%)", name, rep.Pct))
		case runnersvc.ClearFlag:
			st.BlockedByUs, st.BlockReason = false, ""
			did = append(did, name+" was restarted by someone else; flag cleared")
		}
	}
	switch {
	case err != nil:
		warn("service %s status: %v; blocking skipped", name, err)
	case !exists:
		warn("service %s not found; blocking skipped", name)
	case rep.DockerHealthy && !measured:
		warn("disk usage unknown; blocking skipped")
	default:
		decide()
		// A Docker block whose disk is now over the limit is a disk block:
		// only that lifts at prune_percent and lets reclaim run.
		if st.BlockedByUs && !running && rep.DockerHealthy && st.BlockReason == runnersvc.ReasonDocker && rep.Pct >= d.Cfg.BlockPercent {
			st.BlockReason = runnersvc.ReasonDisk
		}
		// Docker Desktop: with the runner stopped for disk, compacting the
		// data disk (which stops Docker) cannot hurt a job.
		if d.Cfg.DesktopReclaim && rep.DockerHealthy && !running && st.BlockedByUs &&
			st.BlockReason == runnersvc.ReasonDisk && now.Sub(st.LastReclaim) >= ReclaimEvery {
			st.LastReclaim = now
			desc, err := d.reclaim(ctx)
			switch {
			case err != nil:
				warn("reclaim: %v", err)
			case desc != "":
				rep.Reclaimed = desc
				did = append(did, desc)
			}
			rep.DockerHealthy = dockerctl.Healthy(ctx, d.R)
			waitHealthy()
			if rep.DockerHealthy {
				measure(" after reclaim")
				if measured {
					decide()
				}
			}
		}
	}

	// Pressure is reported, never acted on: the guard cannot tell a heavy job
	// from a sick host. Page once when it lasts SustainedRuns runs in a row.
	latency := rep.DockerLatency
	if !rep.DockerHealthy {
		latency = 0 // a dead engine is handled above, not a slowness signal
	}
	if s, err := d.MeasurePressure(); err != nil {
		logf("pressure: %v", err)
	} else {
		rep.Pressure = s
		reasons := pressure.Reasons(s, latency)
		if len(reasons) == 0 {
			st.PressureRuns = 0
		} else {
			st.PressureRuns++
			logf("pressure (%d run(s) in a row): %s", st.PressureRuns, strings.Join(reasons, ", "))
			if st.PressureRuns == pressure.SustainedRuns {
				did = append(did, fmt.Sprintf("host under pressure for %d runs: %s", st.PressureRuns, strings.Join(reasons, ", ")))
			}
		}
	}

	if d.HistoryPath != "" {
		if rep.DockerHealthy && measured {
			r := tune.Sample{T: now.UTC(), Kind: "run", Disk: firstPct, DiskEnd: rep.Pct,
				Skip: tune.SkipReason(st, now, rep.DockerHealthy, running)}
			if err := tune.Append(d.HistoryPath, r); err != nil {
				logf("warning: history: %v", err)
			}
		}
		if rep.DockerHealthy && !st.BlockedByUs && now.Sub(st.LastTune) >= TuneEvery {
			msgs, retry := dailyTune(ctx, d, &st, now, logf)
			if !retry { // a change deferred for a busy runner is tried again next hour
				st.LastTune = now
			}
			did = append(did, msgs...)
		}
	}

	// Nothing new happened but something is still broken: say so every few hours.
	if len(notable(did)) == 0 && now.Sub(st.LastRemind) >= RemindEvery {
		switch {
		case st.BlockedByUs && now.Sub(st.BlockedAt) >= RemindEvery:
			did = append(did, fmt.Sprintf("%s still stopped by runner-guard (%s) since %s", name, st.BlockReason, st.BlockedAt.Format(time.RFC3339)))
		case !rep.DockerHealthy:
			did = append(did, "Docker still not responding")
		}
		if len(notable(did)) > 0 {
			st.LastRemind = now
		}
	}

	st.DockerRestarts = dockerctl.Trim(now, st.DockerRestarts)
	st.LastRun = now
	if len(did) > 0 {
		st.LastAction = now.Format(time.RFC3339) + " " + strings.Join(did, "; ")
	}
	if err := state.Save(d.StatePath, st); err != nil {
		warn("save state: %v", err)
	}
	logf("run done: docker_healthy=%v docker_latency=%s restarted=%v disk=%.1f%% pruned=%v stuck_removed=%d mem=%.0f%% load_per_cpu=%.2f action=%s warnings=%d",
		rep.DockerHealthy, rep.DockerLatency.Round(time.Millisecond), rep.Restarted, rep.Pct, rep.Pruned, len(rep.Stuck),
		rep.Pressure.MemPct, rep.Pressure.LoadPerCPU, rep.Action, len(rep.Warnings))
	if n := notable(did); len(n) > 0 && d.Alert != nil {
		host, _ := os.Hostname()
		if err := d.Alert(ctx, fmt.Sprintf("[runner-guard] %s: %s", host, strings.Join(n, "; "))); err != nil {
			logf("warning: alert: %v", err)
		}
	}
	return rep, nil
}

// recoverDocker runs once the daily restart cap is used up: a WSL restart
// (Windows), then, if allowed, one reboot. A reboot that did not help is
// never repeated until Docker has answered again.
func (d Deps) recoverDocker(ctx context.Context, st *state.State, now time.Time, rep *Report, wait func(), warn func(string, ...any)) []string {
	if st.RebootPending {
		if now.Sub(st.LastRemind) >= RemindEvery {
			st.LastRemind = now
			return []string{"Docker still dead after the recovery reboot and restarts; no more reboots, needs a person"}
		}
		return nil
	}
	if now.Sub(st.LastWSLRecover) >= ReclaimEvery {
		if err := osRecover(ctx, d); !errors.Is(err, errors.ErrUnsupported) {
			st.LastWSLRecover = now
			rep.DockerHealthy = dockerctl.Healthy(ctx, d.R)
			wait()
			if rep.DockerHealthy {
				return []string{"Docker back after a WSL restart"}
			}
			warn("WSL restart did not bring Docker back: %v", err)
		}
	}
	if !d.Cfg.RebootRecover || now.Sub(st.LastReboot) < RebootEvery {
		return nil
	}
	if err := canReboot(ctx, d); err != nil {
		warn("no recovery reboot: %v", err)
		return nil
	}
	st.RebootPending, st.LastReboot = true, now
	if err := state.Save(d.StatePath, *st); err != nil {
		st.RebootPending = false
		warn("save state before reboot: %v; not rebooting", err)
		return nil
	}
	if err := reboot(ctx, d); err != nil {
		st.RebootPending = false
		return []string{fmt.Sprintf("recovery reboot FAILED: %v", err)}
	}
	return []string{"Docker dead after the daily restarts; rebooting in 2 minutes"}
}

// notable drops pruning, which repeats hourly at high usage; not worth a ping.
func notable(did []string) []string {
	var n []string
	for _, s := range did {
		if !strings.HasPrefix(s, "pruned") {
			n = append(n, s)
		}
	}
	return n
}

// stopRunner is the only way runner-guard stops the runner. It records the
// block first, so a crash, a failed save or a failed start afterwards never
// leaves a stopped runner that no run will start again. Every reason needs a
// release rule in runnersvc.Decide. An error means the runner still runs.
func (d Deps) stopRunner(ctx context.Context, st *state.State, reason string, now time.Time) error {
	name := d.Cfg.RunnerService
	st.BlockedByUs, st.BlockReason, st.BlockedAt = true, reason, now
	if err := state.Save(d.StatePath, *st); err != nil {
		st.BlockedByUs, st.BlockReason = false, ""
		return fmt.Errorf("save state before stopping %s: %v; not stopping", name, err)
	}
	if err := runnersvc.Stop(ctx, d.R, name); err != nil {
		// A slow stop may still finish; keep the flag unless it is still running.
		if still, _, serr := runnersvc.Status(ctx, d.R, name); serr == nil && still {
			st.BlockedByUs, st.BlockReason = false, ""
			state.Save(d.StatePath, *st)
			return fmt.Errorf("stop %s: %v", name, err)
		}
	}
	return nil
}
