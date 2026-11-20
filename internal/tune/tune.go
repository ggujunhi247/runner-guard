package tune

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/state"
)

// Knob names, as used in state.json and reports.
const (
	Concurrent = "concurrent"
	WSLMemory  = "wsl_memory_gb"
	WSLSwap    = "wsl_swap_gb"
	Prune      = "prune_percent"
)

// Gates and steps. Raises are slow and need data gathered at the current
// value; lowering on pressure is fast; a change that hurts within Watch is
// reverted and its value becomes a ceiling; two reverts freeze the knob.
const (
	Interval      = 5 * time.Minute
	FirstGate     = 14 * 24 * time.Hour // data at the starting value before the first change
	NextGate      = 3 * 24 * time.Hour  // data at a value we set before the next raise
	WSLGate       = 7 * 24 * time.Hour
	Watch         = 72 * time.Hour
	FirstJobHours = 20
	NextJobHours  = 5
	MinCoverage   = 0.5  // usable samples / expected; hosts that sleep at night still pass
	Saturated     = 0.25 // share of busy samples running at the limit before a raise
	ReserveMB     = 2048 // kept free beyond other containers' p99
	WSLStepGB     = 2
	WSLMinGB      = 4
	HostKeepGB    = 4 // never give WSL more than host RAM minus this
	PruneFloor    = 60
	PruneStep     = 2
	LeadRuns      = 3 // prune must start this many hourly runs before block
	swapInPages   = 25600
)

type Change struct {
	Knob     string
	From, To float64
	Why      string
	Revert   bool
}

func (c Change) String() string {
	s := fmt.Sprintf("%s %g -> %g (%s)", c.Knob, c.From, c.To, c.Why)
	if c.Revert {
		s += " [revert]"
	}
	return s
}

// Plan lists changes in priority order; one is applied per day.
type Plan struct {
	Changes []Change
	Notes   []string
}

// Input is everything Decide looks at.
type Input struct {
	History []Sample
	State   state.State
	Cfg     config.Config
	Now     time.Time
	Cores   int
	Windows bool
	// Current settings read from the files; 0 = unknown. MemTotal inside the VM
	// is ~2% below .wslconfig's memory=, so rounding it drifts (32GB reads 31).
	Concurrent          int
	WSLMemGB, WSLSwapGB float64
}

// Decide is pure: history and state in, proposed changes out.
func Decide(in Input) Plan {
	var p Plan
	samples, runs := split(in.History)
	if len(samples) == 0 {
		p.Notes = append(p.Notes, "no samples yet")
	}
	quiet := quietSince(in.State, in.Now)
	if c, note := decideConcurrent(in, samples, quiet); c != nil {
		p.Changes = append(p.Changes, *c)
	} else if note != "" {
		p.Notes = append(p.Notes, note)
	}
	if in.Windows {
		for _, c := range decideWSL(in, samples, quiet) {
			p.Changes = append(p.Changes, c)
		}
	} else if note := linuxSwapNote(samples, in.Now); note != "" {
		p.Notes = append(p.Notes, note)
	}
	if c, note := decidePrune(in, runs); c != nil {
		p.Changes = append(p.Changes, *c)
	} else if note != "" {
		p.Notes = append(p.Notes, note)
	}
	// Reverts and lowering first: they undo harm.
	sort.SliceStable(p.Changes, func(i, j int) bool { return rank(p.Changes[i]) < rank(p.Changes[j]) })
	return p
}

func rank(c Change) int {
	switch {
	case c.Revert:
		return 0
	case c.Knob == WSLMemory && c.To > c.From: // keeps throughput, so before cutting concurrent
		return 1
	case c.To < c.From:
		return 2
	}
	return 3
}

func split(h []Sample) (samples, runs []Sample) {
	for _, s := range h {
		if !s.usable() {
			continue
		}
		if s.Kind == "run" {
			runs = append(runs, s)
		} else {
			samples = append(samples, s)
		}
	}
	return
}

// quietSince: raises wait until no knob changed within Watch (one probe at a time).
func quietSince(st state.State, now time.Time) bool {
	for _, k := range []state.Knob{st.Concurrent, st.WSLMemoryGB, st.WSLSwapGB, st.PrunePercent} {
		if !k.Since.IsZero() && now.Sub(k.Since) < Watch {
			return false
		}
	}
	return true
}

func lastChange(st state.State) time.Time {
	var t time.Time
	for _, k := range []state.Knob{st.Concurrent, st.WSLMemoryGB, st.WSLSwapGB, st.PrunePercent} {
		if k.Since.After(t) {
			t = k.Since
		}
	}
	return t
}

func after(h []Sample, t time.Time) []Sample {
	i := sort.Search(len(h), func(i int) bool { return h[i].T.After(t) })
	return h[i:]
}

// stress returns why the samples show memory trouble while jobs ran, or "".
func stress(h []Sample) string {
	var high, swap, oom int
	for i, s := range h {
		if s.Jobs == 0 {
			continue
		}
		if s.MemPct() >= 90 {
			high++
		}
		if i > 0 && s.T.Sub(h[i-1].T) <= 3*Interval && s.MemTotalMB > 0 && h[i-1].MemTotalMB > 0 {
			if d := s.Pswpin - h[i-1].Pswpin; d > swapInPages {
				swap++
			}
			if s.OOMKill > h[i-1].OOMKill {
				oom++
			}
		}
	}
	switch {
	case oom > 0:
		return fmt.Sprintf("%d OOM kill sample(s)", oom)
	case swap >= 3:
		return fmt.Sprintf("heavy swap-in in %d samples", swap)
	case high >= 3:
		return fmt.Sprintf("memory >= 90%% in %d samples", high)
	}
	return ""
}

// gate reports whether h holds enough data since `since` to judge a value.
func gate(h []Sample, since, now time.Time, span time.Duration, jobHours float64) (bool, string) {
	if len(h) == 0 {
		return false, "no data at this value"
	}
	start := h[0].T
	if since.After(start) {
		start = since
	}
	var jh float64
	for _, s := range h {
		jh += float64(s.Jobs) * Interval.Hours()
	}
	got := now.Sub(start)
	cov := float64(len(h)) / (got.Hours() / Interval.Hours())
	if got < span || jh < jobHours || cov < MinCoverage {
		return false, fmt.Sprintf("collecting (%.1f/%.0f days, %.1f/%.0f job-hours, coverage %.0f%%)",
			got.Hours()/24, span.Hours()/24, jh, jobHours, math.Min(cov, 1)*100)
	}
	return true, ""
}

func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	v = append([]float64(nil), v...)
	sort.Float64s(v)
	return v[int(math.Ceil(p/100*float64(len(v))))-1]
}

func decideConcurrent(in Input, h []Sample, quiet bool) (*Change, string) {
	if len(h) == 0 {
		return nil, ""
	}
	cur := in.Concurrent // read from config.toml now; samples can be hours old
	if cur <= 0 {
		cur = h[len(h)-1].Concurrent
	}
	if cur <= 0 {
		return nil, "concurrent: runner config unreadable (" + in.Cfg.RunnerConfig + ")"
	}
	k := in.State.Concurrent
	var at []Sample
	for _, s := range after(h, k.Since) {
		if s.Concurrent == cur {
			at = append(at, s)
		}
	}
	mk := func(to int, why string, revert bool) *Change {
		return &Change{Knob: Concurrent, From: float64(cur), To: float64(to), Why: why, Revert: revert}
	}
	if why := stress(after(at, in.Now.Add(-24*time.Hour))); why != "" && cur > 1 {
		revert := k.Raised && k.Value == float64(cur) && in.Now.Sub(k.Since) < Watch
		return mk(cur-1, why, revert), ""
	}
	if cur > in.Cores && in.Cores > 0 {
		return mk(cur-1, fmt.Sprintf("above %d CPU cores", in.Cores), false), ""
	}
	if k.Frozen {
		return nil, "concurrent: frozen after 2 reverts (runner-guard tune --reset)"
	}
	span, jh := FirstGate, float64(FirstJobHours)
	if !k.Since.IsZero() {
		span, jh = NextGate, NextJobHours
	}
	if ok, why := gate(at, k.Since, in.Now, span, jh); !ok {
		return nil, "concurrent " + fmt.Sprint(cur) + ": " + why
	}
	var busy, full int
	var jobMem, other, total []float64
	for _, s := range at {
		if s.Jobs > 0 {
			busy++
			if s.Jobs >= cur {
				full++
			}
		}
		jobMem = append(jobMem, s.JobMemMB...)
		other = append(other, s.OtherMemMB)
		if s.MemTotalMB > 0 {
			total = append(total, s.MemTotalMB)
		}
	}
	upper := in.Cores
	if p95 := percentile(jobMem, 95); p95 > 0 && len(total) > 0 {
		fit := int((percentile(total, 50) - percentile(other, 99) - ReserveMB) / p95)
		if cur > max(fit, 1) {
			return mk(cur-1, fmt.Sprintf("jobs need ~%.0fMB each; memory fits %d", p95, max(fit, 1)), false), ""
		}
		upper = min(upper, fit)
	} else {
		return nil, fmt.Sprintf("concurrent %d: no per-job memory measured yet", cur)
	}
	if k.Ceiling > 0 {
		upper = min(upper, int(k.Ceiling)-1)
	}
	sat := float64(full) / float64(max(busy, 1))
	switch {
	case sat < Saturated:
		return nil, fmt.Sprintf("concurrent %d: at the limit in %.0f%% of busy samples; no raise needed", cur, sat*100)
	case cur+1 > upper:
		return nil, fmt.Sprintf("concurrent %d: busy (%.0f%% at the limit) but %d is the memory/CPU/ceiling bound", cur, sat*100, upper)
	case !quiet:
		return nil, fmt.Sprintf("concurrent %d: would raise; waiting for the last change to settle", cur)
	}
	return mk(cur+1, fmt.Sprintf("at the limit in %.0f%% of busy samples", sat*100), false), ""
}

// decideWSL sizes the Docker Desktop VM in 2GB steps, at most weekly.
func decideWSL(in Input, h []Sample, quiet bool) []Change {
	week := after(h, in.Now.Add(-WSLGate))
	if len(week) == 0 || week[len(week)-1].MemTotalMB <= 0 || week[len(week)-1].HostMemMB <= 0 {
		return nil
	}
	last := week[len(week)-1]
	curMem := math.Round(last.MemTotalMB / 1024)
	if in.WSLMemGB > 0 {
		curMem = math.Round(in.WSLMemGB) // whole GB, as written; else our own write looks like a hand edit
	}
	hostGB := last.HostMemMB / 1024
	var out []Change
	km, ks := in.State.WSLMemoryGB, in.State.WSLSwapGB
	if !km.Since.IsZero() && in.Now.Sub(km.Since) < WSLGate || !ks.Since.IsZero() && in.Now.Sub(ks.Since) < WSLGate {
		// Still weekly, but a raise that hurts the host is undone at once.
		if km.Raised && in.Now.Sub(km.Since) < Watch && hostP95(after(h, km.Since)) >= 95 {
			out = append(out, Change{Knob: WSLMemory, From: curMem, To: km.Prev, Why: "host memory >= 95% after the raise", Revert: true})
		}
		return out
	}
	span := FirstGate
	if !km.Since.IsZero() {
		span = WSLGate
	}
	if ok, _ := gate(after(h, km.Since), km.Since, in.Now, span, FirstJobHours/2); !ok {
		return nil
	}
	// Stress from before the last change of any knob may already be fixed.
	why := stress(after(week, lastChange(in.State)))
	host := hostP95(week)
	var used []float64
	for _, s := range week {
		used = append(used, (s.MemTotalMB-s.MemAvailMB)/1024)
	}
	switch {
	case why != "" && !km.Frozen && host < 80 && curMem+WSLStepGB <= hostGB-HostKeepGB &&
		(km.Ceiling == 0 || curMem+WSLStepGB < km.Ceiling) && quiet:
		out = append(out, Change{Knob: WSLMemory, From: curMem, To: curMem + WSLStepGB, Why: why})
	case why == "" && quiet && host >= 85 && percentile(used, 99)+WSLStepGB+2 < curMem && curMem-WSLStepGB >= WSLMinGB:
		out = append(out, Change{Knob: WSLMemory, From: curMem, To: curMem - WSLStepGB, Why: fmt.Sprintf("host memory p95 %.0f%%; the VM never used %.0fGB", host, curMem-WSLStepGB)})
	}
	// Swap only grows, and only after OOM kills with swap nearly used up.
	var oomFull bool
	for i, s := range week {
		if i > 0 && s.OOMKill > week[i-1].OOMKill && s.SwapMB > 0 && s.SwapFreeMB < s.SwapMB/10 {
			oomFull = true
		}
	}
	curSwap := math.Round(last.SwapMB / 1024)
	if in.WSLSwapGB > 0 {
		curSwap = math.Round(in.WSLSwapGB)
	}
	if oomFull && !ks.Frozen && quiet && curSwap+WSLStepGB <= 16 {
		out = append(out, Change{Knob: WSLSwap, From: curSwap, To: curSwap + WSLStepGB, Why: "OOM kills with swap nearly full"})
	}
	return out
}

func hostP95(h []Sample) float64 {
	var v []float64
	for _, s := range h {
		if s.HostMemPct > 0 {
			v = append(v, s.HostMemPct)
		}
	}
	return percentile(v, 95)
}

// Linux swap on a shared host is a human decision; only say so.
func linuxSwapNote(h []Sample, now time.Time) string {
	week := after(h, now.Add(-WSLGate))
	if len(week) == 0 || week[len(week)-1].SwapMB > 0 {
		return ""
	}
	if why := stress(week); why != "" {
		return "no swap and " + why + " this week; consider adding swap (not done automatically)"
	}
	return ""
}

// decidePrune lowers prune_percent (never below PruneFloor, never raises) so
// cleanup starts LeadRuns hourly runs before the block threshold at the
// fastest observed growth.
func decidePrune(in Input, runs []Sample) (*Change, string) {
	k := in.State.PrunePercent
	eff := in.Cfg.PrunePercent
	if k.Value > 0 && k.Value < eff {
		eff = k.Value
	}
	if eff <= PruneFloor {
		return nil, ""
	}
	if !k.Since.IsZero() && in.Now.Sub(k.Since) < 24*time.Hour {
		return nil, ""
	}
	if len(runs) < 100 || in.Now.Sub(runs[0].T) < FirstGate/2 {
		return nil, fmt.Sprintf("prune_percent: collecting (%d/100 hourly readings)", len(runs))
	}
	var rises []float64
	for i := 1; i < len(runs); i++ {
		prev := runs[i-1].DiskEnd
		if prev == 0 {
			prev = runs[i-1].Disk
		}
		if d := runs[i].Disk - prev; d > 0 && runs[i].T.Sub(runs[i-1].T) <= 2*time.Hour {
			rises = append(rises, d)
		}
	}
	lead := LeadRuns * percentile(rises, 90)
	if in.Cfg.BlockPercent-eff >= lead {
		return nil, ""
	}
	to := math.Max(PruneFloor, eff-PruneStep)
	return &Change{Knob: Prune, From: eff, To: to,
		Why: fmt.Sprintf("disk grows up to %.1f%%/run; need %.1f points of lead before %g", lead/LeadRuns, lead, in.Cfg.BlockPercent)}, ""
}

// SyncKnob forgets a knob whose value someone changed by hand.
func SyncKnob(k *state.Knob, actual float64) bool {
	if k.Value != 0 && math.Abs(k.Value-actual) > 0.5 {
		*k = state.Knob{}
		return true
	}
	return false
}

// Record updates the knob after c was applied at now.
func Record(k *state.Knob, c Change, now time.Time) {
	if k.Value == 0 && k.Base == 0 {
		k.Base = c.From // first change: for prune this is the config value
	}
	if c.Revert {
		k.Ceiling = c.From // the raised value failed
		k.Reverts++
		k.Frozen = k.Reverts >= 2
	} else if k.Raised && k.Value == c.From && c.To < c.From && (k.Ceiling == 0 || c.From < k.Ceiling) {
		// Lowering a value we raised, even after Watch: don't raise into it again
		// next week (a weekly heavy pipeline would otherwise oscillate forever).
		k.Ceiling = c.From
	}
	k.Prev, k.Value, k.Since, k.Raised = c.From, c.To, now, c.To > c.From
}
