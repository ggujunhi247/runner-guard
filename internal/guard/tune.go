package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/state"
	"github.com/ggujunhi247/runner-guard/internal/tune"
)

// TuneEvery is how often the tune pass runs inside run.
const TuneEvery = 24 * time.Hour

// errBusy: a change that needs an idle runner found jobs; try another day.
var errBusy = errors.New("runner busy")

// KillSwitch is the file that stops all automatic tuning when present.
func KillSwitch(dir string) string { return filepath.Join(dir, "tune.disabled") }

// TunePlan loads history and decides; it changes nothing.
func TunePlan(d Deps, st *state.State, now time.Time) (tune.Plan, error) {
	h, err := tune.Load(d.HistoryPath, now.Add(-tune.Keep))
	if err != nil {
		return tune.Plan{}, err
	}
	in := tune.Input{History: h, Cfg: d.Cfg, Now: now, Cores: runtime.NumCPU(), Windows: runtime.GOOS == "windows"}
	var notes []string
	// The files are the truth; a hand edit restarts that knob's tuning.
	if c, err := tune.ReadConcurrent(d.Cfg.RunnerConfig); err == nil {
		in.Concurrent = c
		if tune.SyncKnob(&st.Concurrent, float64(c)) {
			notes = append(notes, "concurrent was changed by hand; tuning starts over from it")
		}
	}
	if in.Windows {
		if home, err := os.UserHomeDir(); err == nil {
			b, _ := os.ReadFile(filepath.Join(home, ".wslconfig"))
			in.WSLMemGB, in.WSLSwapGB = tune.WSLValue(b, "memory"), tune.WSLValue(b, "swap")
			for _, k := range []struct {
				k *state.Knob
				v float64
			}{{&st.WSLMemoryGB, in.WSLMemGB}, {&st.WSLSwapGB, in.WSLSwapGB}} {
				if k.v > 0 && tune.SyncKnob(k.k, k.v) {
					notes = append(notes, ".wslconfig was changed by hand; WSL tuning starts over from it")
				}
			}
		}
	}
	if k := &st.PrunePercent; k.Value > 0 && k.Base == 0 {
		k.Base = d.Cfg.PrunePercent // learned by a version that did not record it
	} else if k.Base > 0 && k.Base != d.Cfg.PrunePercent {
		*k = state.Knob{}
		notes = append(notes, "prune_percent was changed in config; the learned value is dropped")
	}
	in.State = *st
	p := tune.Decide(in)
	p.Notes = append(notes, p.Notes...)
	return p, nil
}

// dailyTune applies at most one change per day and returns what to alert on.
// retry is true when a change was deferred because the runner was busy.
func dailyTune(ctx context.Context, d Deps, st *state.State, now time.Time, logf func(string, ...any)) (msgs []string, retry bool) {
	h, err := tune.Load(d.HistoryPath, now.Add(-tune.Keep))
	if err == nil && len(h) > 0 {
		if err := tune.Rewrite(d.HistoryPath, h); err != nil {
			logf("warning: trim history: %v", err)
		}
	}
	p, err := TunePlan(d, st, now)
	if err != nil {
		logf("warning: tune: %v", err)
		return nil, false
	}
	for _, n := range p.Notes {
		logf("tune: %s", n)
	}
	active := d.Cfg.AutoTune && !d.exists(KillSwitch(filepath.Dir(d.HistoryPath)))
	if !active {
		var would []string
		for _, c := range p.Changes {
			would = append(would, c.String())
		}
		report := strings.Join(would, "; ")
		if report != "" {
			logf("tune (report only): would change %s", report)
		}
		if report != "" && report != st.LastProposal {
			st.LastProposal = report
			return []string{"tune (report only) would change " + report}, false
		}
		st.LastProposal = report
		return nil, false
	}
	var revertWaits bool
	for _, c := range p.Changes {
		k := knob(st, c.Knob)
		// Frozen stops raises and WSL restarts; a cheap lowering under stress still goes through.
		if k.Frozen && (c.To > c.From || c.Knob == tune.WSLMemory || c.Knob == tune.WSLSwap) {
			logf("tune: %s skipped: %s is frozen (runner-guard tune --reset)", c, c.Knob)
			continue
		}
		err := d.applyChange(ctx, st, c, now)
		if errors.Is(err, errBusy) {
			logf("tune: %s deferred: %v", c, err)
			retry = true
			revertWaits = revertWaits || c.Revert
			continue
		}
		if err != nil {
			k.Frozen = true
			return []string{fmt.Sprintf("tune %s FAILED (previous setting kept or restored); %s frozen until `runner-guard tune --reset`: %v", c, c.Knob, err)}, false
		}
		tune.Record(k, c, now)
		msg := "tuned " + c.String()
		if k.Frozen {
			msg += fmt.Sprintf("; %s frozen after 2 reverts", c.Knob)
		}
		return []string{msg}, revertWaits // only undoing harm keeps the hourly retry
	}
	return nil, retry
}

func knob(st *state.State, name string) *state.Knob {
	switch name {
	case tune.Concurrent:
		return &st.Concurrent
	case tune.WSLMemory:
		return &st.WSLMemoryGB
	case tune.WSLSwap:
		return &st.WSLSwapGB
	}
	return &st.PrunePercent
}

func (d Deps) applyChange(ctx context.Context, st *state.State, c tune.Change, now time.Time) error {
	if d.Apply != nil {
		return d.Apply(ctx, c)
	}
	switch c.Knob {
	case tune.Concurrent:
		// gitlab-runner reloads config.toml by itself within about a minute.
		return tune.SetConcurrent(d.Cfg.RunnerConfig, int(c.From), int(c.To))
	case tune.Prune:
		return nil // used from state by the next run
	case tune.WSLMemory, tune.WSLSwap:
		// One disruptive action a day, shared with VHDX reclaim; undoing a
		// harmful raise does not wait for the budget.
		if st.BlockedByUs || !c.Revert && (now.Sub(st.LastReclaim) < ReclaimEvery || now.Sub(st.LastDisruptive) < ReclaimEvery) {
			return fmt.Errorf("%w: daily restart budget used or runner blocked", errBusy)
		}
		key := "memory"
		if c.Knob == tune.WSLSwap {
			key = "swap"
		}
		return applyWSL(ctx, d, st, key, int(c.To), now)
	}
	return fmt.Errorf("unknown knob %s", c.Knob)
}

// PrunePercent is prune_percent after any learned lowering.
func PrunePercent(d Deps, st state.State) float64 {
	if v := st.PrunePercent.Value; v > 0 && v < d.Cfg.PrunePercent {
		return v
	}
	return d.Cfg.PrunePercent
}
