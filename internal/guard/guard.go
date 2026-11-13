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

	"github.com/ggujunhi/runner-guard/internal/config"
	"github.com/ggujunhi/runner-guard/internal/disk"
	"github.com/ggujunhi/runner-guard/internal/dockerctl"
	"github.com/ggujunhi/runner-guard/internal/execx"
	"github.com/ggujunhi/runner-guard/internal/runnersvc"
	"github.com/ggujunhi/runner-guard/internal/state"
)

// LockStale is how old a lock must be before another run takes it over.
const LockStale = 2 * time.Hour

// After a restart, wait up to restartWaits*restartPoll for the engine.
const (
	restartWaits = 18
	restartPoll  = 10 * time.Second
)

type Deps struct {
	R                   execx.Runner
	Cfg                 config.Config
	StatePath, LockPath string
	Now                 func() time.Time
	Log                 io.Writer
	Exists              func(string) bool
	// Usage overrides disk.Usage (tests). Nil uses disk.Usage.
	Usage func(context.Context) (float64, string, error)
	// Sleep waits between health polls after a restart. Nil means no wait.
	Sleep func(time.Duration)
}

type Report struct {
	DockerHealthy bool
	Restarted     bool
	Pct           float64
	Pruned        bool
	Action        runnersvc.Action
	Warnings      []string
}

// MeasureUsage returns the disk used percent for this host.
func (d Deps) MeasureUsage(ctx context.Context) (float64, string, error) {
	if d.Usage != nil {
		return d.Usage(ctx)
	}
	return disk.Usage(ctx, d.R, d.Cfg)
}

func (d Deps) exists(p string) bool {
	if d.Exists != nil {
		return d.Exists(p)
	}
	_, err := os.Stat(p)
	return err == nil
}

// Run: refuse → lock → state → Docker → disk → runner service → save.
func Run(ctx context.Context, d Deps, force bool) (Report, error) {
	var rep Report
	for _, p := range d.Cfg.RefuseIfExists {
		if d.exists(p) {
			return rep, fmt.Errorf("refusing to run: %s exists (refuse_if_exists)", p)
		}
	}
	_, restartMin, err := d.Cfg.Durations()
	if err != nil {
		return rep, err
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
	var did []string

	// Docker first: without an engine, pruning is pointless.
	err = dockerctl.Check(ctx, d.R)
	if errors.Is(err, execx.ErrNotFound) {
		// A misconfigured guard must not take the runner down.
		return rep, fmt.Errorf("docker CLI not found in PATH; nothing changed: %w", err)
	}
	rep.DockerHealthy = err == nil
	if !rep.DockerHealthy {
		logf("docker unhealthy: %v", err)
		if dockerctl.CanRestart(now, st.DockerRestarts, restartMin, d.Cfg.DockerRestartMaxPerDay) {
			st.DockerRestarts = append(st.DockerRestarts, now)
			rep.Restarted = true
			did = append(did, "restarted docker")
			if err := dockerctl.Restart(ctx, d.R); err != nil {
				warn("docker restart failed: %v", err)
			} else {
				for i := 0; i < restartWaits && !rep.DockerHealthy; i++ {
					if d.Sleep != nil {
						d.Sleep(restartPoll)
					}
					rep.DockerHealthy = dockerctl.Healthy(ctx, d.R)
				}
			}
		} else {
			warn("docker unhealthy and restart not allowed (interval or daily cap)")
		}
	}

	measured := false
	if rep.DockerHealthy {
		pct, desc, err := d.MeasureUsage(ctx)
		if err != nil {
			warn("disk usage: %v", err)
		} else {
			measured = true
			rep.Pct = pct
			logf("disk %s", desc)
			if disk.ShouldPrune(pct, d.Cfg.PrunePercent, force) {
				rep.Pruned = true
				did = append(did, fmt.Sprintf("pruned at %.1f%%", pct))
				if err := disk.Prune(ctx, d.R, d.Cfg); err != nil {
					warn("prune: %v", err)
				}
				if pct, desc, err = d.MeasureUsage(ctx); err != nil {
					warn("disk usage after prune: %v", err)
					measured = false
				} else {
					rep.Pct = pct
					logf("disk after prune %s", desc)
				}
			}
		}
	}

	name := d.Cfg.RunnerService
	running, exists, err := runnersvc.Status(ctx, d.R, name)
	switch {
	case err != nil:
		warn("service %s status: %v; blocking skipped", name, err)
	case !exists:
		warn("service %s not found; blocking skipped", name)
	case rep.DockerHealthy && !measured:
		warn("disk usage unknown; blocking skipped")
	default:
		rep.Action = runnersvc.Decide(rep.DockerHealthy, rep.Pct, d.Cfg.PrunePercent, d.Cfg.BlockPercent, running, st.BlockedByUs)
		switch rep.Action {
		case runnersvc.Block:
			if err := runnersvc.Stop(ctx, d.R, name); err != nil {
				warn("stop %s: %v", name, err)
			} else {
				st.BlockedByUs = true
				did = append(did, fmt.Sprintf("stopped %s (docker healthy=%v, disk %.1f%%)", name, rep.DockerHealthy, rep.Pct))
			}
		case runnersvc.Unblock:
			if err := runnersvc.Start(ctx, d.R, name); err != nil {
				warn("start %s: %v", name, err)
			} else {
				st.BlockedByUs = false
				did = append(did, "started "+name)
			}
		case runnersvc.ClearFlag:
			st.BlockedByUs = false
			did = append(did, name+" was restarted by someone else; flag cleared")
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
	logf("run done: docker_healthy=%v restarted=%v disk=%.1f%% pruned=%v action=%s warnings=%d",
		rep.DockerHealthy, rep.Restarted, rep.Pct, rep.Pruned, rep.Action, len(rep.Warnings))
	return rep, nil
}
