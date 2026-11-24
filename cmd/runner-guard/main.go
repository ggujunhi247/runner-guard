// Command runner-guard keeps a GitLab Runner (docker executor) host healthy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/alert"
	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/disk"
	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/doctor"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/fsx"
	"github.com/ggujunhi247/runner-guard/internal/guard"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/schedule"
	"github.com/ggujunhi247/runner-guard/internal/state"
	"github.com/ggujunhi247/runner-guard/internal/tune"
	"github.com/ggujunhi247/runner-guard/internal/update"
)

const usage = `usage: runner-guard <command>

  check | status [--json]  show disk, Docker and runner state; change nothing
  run [--force]            prune, repair Docker, block/unblock the runner
  update [--check]         install the latest release (checksum verified)
  sample                   record one usage sample (scheduled every 5 min)
  tune [--reset]           show what the daily tune would change; --reset
                           forgets learned values, ceilings and freezes
  doctor                   check, read-only, that restarts, WSL changes, recovery
                           reboot and tasks would work here (run on every new host)
  install                  copy binary, write default config, schedule run/update
  uninstall                remove the scheduled tasks (config and logs stay)
  selftest                 check built-in decision logic
  version
`

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON output (status)")
	force := fs.Bool("force", false, "prune even below prune_percent (run)")
	checkOnly := fs.Bool("check", false, "only report whether an update exists (update)")
	reset := fs.Bool("reset", false, "forget tuning state (tune)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if msg := needsRoot(cmd, *checkOnly, *reset); msg != "" {
		fmt.Fprintln(os.Stderr, "[runner-guard] error:", msg)
		return 1
	}
	var err error
	switch cmd {
	case "version":
		fmt.Println(update.Version)
	case "selftest":
		if err = selftest(); err == nil {
			fmt.Println("[runner-guard] selftest ok")
		}
	case "check", "status":
		var d guard.Deps
		if d, err = deps(ctx, os.Stdout); err == nil {
			err = runStatus(ctx, d, *asJSON, os.Stdout)
		}
	case "run":
		err = withLog(func(w io.Writer) error {
			d, err := deps(ctx, w)
			if err != nil {
				return err
			}
			// Hosts that only ever auto-updated still get the config locked.
			if fileExists(config.File()) {
				if err := schedule.LockFile(ctx, d.R, config.File()); err != nil {
					guard.Logf(w, time.Now(), "warning: lock %s: %v", config.File(), err)
				}
			}
			if bin := config.BinPath(); fileExists(bin) {
				if added, err := schedule.EnsureSample(ctx, d.R, bin); err != nil {
					guard.Logf(w, time.Now(), "warning: sample schedule: %v", err)
				} else if added {
					guard.Logf(w, time.Now(), "added the 5-minute sample schedule")
				}
			}
			_, err = guard.Run(ctx, d, *force)
			if err != nil && !errors.Is(err, state.ErrLocked) && d.Alert != nil {
				host, _ := os.Hostname()
				d.Alert(ctx, fmt.Sprintf("[runner-guard] %s: run failed: %v", host, err))
			}
			return err
		})
	case "update":
		if *checkOnly { // reads nothing local; works without root
			err = doUpdate(ctx, os.Stdout, true)
		} else {
			err = withLog(func(w io.Writer) error { return doUpdate(ctx, w, false) })
		}
	case "sample":
		err = doSample(ctx)
	case "tune":
		var d guard.Deps
		if d, err = deps(ctx, os.Stdout); err == nil {
			err = doTune(d, *reset, os.Stdout)
		}
	case "doctor":
		var d guard.Deps
		if d, err = deps(ctx, os.Stdout); err == nil && doctor.Print(os.Stdout, doctor.Run(ctx, d)) {
			err = errors.New("doctor found problems (FAIL lines above)")
		}
	case "install":
		err = doInstall(ctx)
	case "uninstall":
		err = doUninstall(ctx)
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[runner-guard] error:", err)
		return 1
	}
	return 0
}

func deps(ctx context.Context, log io.Writer) (guard.Deps, error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return guard.Deps{}, err
	}
	d := guard.Deps{
		R: execx.OS{}, Cfg: cfg,
		StatePath: statePath(), LockPath: lockPath(),
		Now: time.Now, Log: log, Sleep: time.Sleep,
		HistoryPath: historyPath(),
	}
	if cfg.AlertWebhook != "" {
		d.Alert = func(ctx context.Context, msg string) error { return alert.Send(ctx, cfg.AlertWebhook, msg) }
	}
	return d, nil
}

// needsRoot explains, before anything fails half-way, that a command which
// writes the state folder or system services needs root. Windows has its
// own elevation messages.
func needsRoot(cmd string, checkOnly, reset bool) string {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return ""
	}
	switch {
	case cmd == "run", cmd == "sample", cmd == "install", cmd == "uninstall",
		cmd == "update" && !checkOnly, cmd == "tune" && reset:
		return cmd + " needs root: sudo runner-guard " + strings.Join(os.Args[1:], " ")
	}
	return ""
}

func statePath() string { return filepath.Join(config.Dir(), "state.json") }
func lockPath() string  { return filepath.Join(config.Dir(), "lock") }
func historyPath() string {
	return filepath.Join(config.Dir(), "history.jsonl")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// loadConfig loads the config and points runner_config at the config.toml
// the runner service really uses when the configured one does not exist.
func loadConfig(ctx context.Context) (config.Config, error) {
	cfg, err := config.Load(config.File())
	if err != nil {
		return cfg, err
	}
	if p := runnersvc.ConfigPath(ctx, execx.OS{}, cfg.RunnerService, cfg.RunnerConfig); p != "" {
		cfg.RunnerConfig = p
	}
	return cfg, nil
}

// doSample takes no lock: it only appends, and run must not delay it.
func doSample(ctx context.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	st, _ := state.Load(statePath())
	return tune.Append(historyPath(), tune.Collect(ctx, execx.OS{}, cfg, st, time.Now()))
}

func doTune(d guard.Deps, reset bool, w io.Writer) error {
	if reset {
		unlock, err := state.Lock(d.LockPath, guard.LockStale)
		if err != nil {
			return err
		}
		defer unlock()
		st, _ := state.Load(d.StatePath)
		st.Concurrent, st.WSLMemoryGB, st.WSLSwapGB, st.PrunePercent = state.Knob{}, state.Knob{}, state.Knob{}, state.Knob{}
		st.LastProposal = ""
		if err := state.Save(d.StatePath, st); err != nil {
			return err
		}
		fmt.Fprintln(w, "[runner-guard] tuning state cleared; prune_percent is back to the config value")
		return nil
	}
	st, _ := state.Load(d.StatePath)
	p, err := guard.TunePlan(d, &st, time.Now())
	if err != nil {
		return err
	}
	mode := "auto (one change per day)"
	if !guard.TuneActive(d) {
		mode = "report only (auto_tune false or tune.disabled present)"
	}
	fmt.Fprintf(w, "tune mode: %s\nprune_percent in use: %g (config %g)\n", mode, guard.PrunePercent(d, st), d.Cfg.PrunePercent)
	for _, n := range guard.Knobs(&st) {
		if k := n.K; k.Value != 0 || k.Frozen {
			fmt.Fprintf(w, "  %s: %g (since %s, ceiling %g, reverts %d, frozen %v)\n", n.Name, k.Value, k.Since.Format(time.RFC3339), k.Ceiling, k.Reverts, k.Frozen)
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintln(w, "  note:", n)
	}
	if len(p.Changes) == 0 {
		fmt.Fprintln(w, "  no change proposed")
	}
	for i, c := range p.Changes {
		fmt.Fprintf(w, "  proposal %d: %s\n", i+1, c)
	}
	return nil
}

// doUninstall removes the schedule and gives back a runner we stopped;
// nothing would ever start it again otherwise.
func doUninstall(ctx context.Context) error {
	cfg, _ := config.Load(config.File())
	var errs []error
	if st, _ := state.Load(statePath()); st.BlockedByUs {
		if err := runnersvc.Start(ctx, execx.OS{}, cfg.RunnerService); err != nil {
			errs = append(errs, fmt.Errorf("start %s that runner-guard stopped: %w", cfg.RunnerService, err))
		} else {
			st.BlockedByUs, st.BlockReason = false, ""
			state.Save(statePath(), st)
			fmt.Println("[runner-guard] started", cfg.RunnerService, "(it was stopped by runner-guard)")
		}
	}
	if err := schedule.Uninstall(ctx, execx.OS{}); err != nil {
		errs = append(errs, err)
	} else {
		fmt.Println("[runner-guard] scheduled tasks removed; config and logs kept in", config.Dir())
	}
	return errors.Join(errs...)
}

// waitLock waits for a run in progress instead of skipping the day's update.
func waitLock(ctx context.Context, max time.Duration) (func(), error) {
	deadline := time.Now().Add(max)
	for {
		unlock, err := state.Lock(lockPath(), guard.LockStale)
		if !errors.Is(err, state.ErrLocked) || time.Now().After(deadline) {
			return unlock, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
}

// withLog sends log lines to stdout (journald on Linux) and the log file.
func withLog(fn func(io.Writer) error) error {
	f, err := guard.OpenLog(config.Dir())
	if err != nil {
		return fmt.Errorf("open log (run as root/administrator?): %w", err)
	}
	defer f.Close()
	w := io.MultiWriter(os.Stdout, f)
	if err := fn(w); err != nil {
		guard.Logf(f, time.Now(), "error: %v", err) // stderr gets it from realMain
		return err
	}
	return nil
}

type statusReport struct {
	Version       string   `json:"version"`
	DockerHealthy bool     `json:"docker_healthy"`
	DiskPercent   float64  `json:"disk_percent"`
	Disk          string   `json:"disk"`
	RunnerService string   `json:"runner_service"`
	RunnerExists  bool     `json:"runner_exists"`
	RunnerRunning bool     `json:"runner_running"`
	BlockedByUs   bool     `json:"blocked_by_us"`
	BlockReason   string   `json:"block_reason"`
	LastRun       string   `json:"last_run"`
	LastAction    string   `json:"last_action"`
	DockerMillis  int64    `json:"docker_latency_ms"`
	MemoryPercent float64  `json:"memory_percent"`
	LoadPerCPU    float64  `json:"load_per_cpu"` // -1 on Windows
	PressureRuns  int      `json:"pressure_runs"`
	RunnerConfig  string   `json:"runner_config"`
	Concurrent    int      `json:"concurrent"` // 0: unreadable
	Samples24h    int      `json:"samples_24h"`
	TuneMode      string   `json:"tune_mode"`
	LastTune      string   `json:"last_tune"`
	Problems      []string `json:"problems"` // setup issues a person should fix
}

func runStatus(ctx context.Context, d guard.Deps, asJSON bool, w io.Writer) error {
	s := statusReport{Version: update.Version, RunnerService: d.Cfg.RunnerService}
	start := time.Now()
	s.DockerHealthy = dockerctl.Healthy(ctx, d.R)
	s.DockerMillis = time.Since(start).Milliseconds()
	if p, err := d.MeasurePressure(); err == nil {
		s.MemoryPercent, s.LoadPerCPU = p.MemPct, p.LoadPerCPU
	}
	s.Disk = "unknown (docker not responding)"
	if s.DockerHealthy {
		pct, desc, err := d.MeasureUsage(ctx)
		if err != nil {
			s.Disk = "error: " + err.Error()
		} else {
			s.DiskPercent, s.Disk = pct, desc
		}
	}
	if running, exists, err := runnersvc.Status(ctx, d.R, d.Cfg.RunnerService); err == nil {
		s.RunnerRunning, s.RunnerExists = running, exists
	}
	st, _ := state.Load(d.StatePath)
	s.BlockedByUs, s.BlockReason, s.LastAction, s.PressureRuns = st.BlockedByUs, st.BlockReason, st.LastAction, st.PressureRuns
	if !st.LastRun.IsZero() {
		s.LastRun = st.LastRun.Format(time.RFC3339)
	}
	statusTune(ctx, d, st, &s)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	docker := "NOT responding"
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		docker += " (or this user may not use Docker: run with sudo)"
	}
	if s.DockerHealthy {
		docker = "healthy"
	}
	runner := "not found"
	if s.RunnerExists && s.RunnerRunning {
		runner = "running"
	} else if s.RunnerExists {
		runner = "stopped"
	}
	blocked := "no"
	if s.BlockedByUs {
		blocked = "yes (" + s.BlockReason + ")"
	}
	load := "n/a"
	if s.LoadPerCPU >= 0 {
		load = fmt.Sprintf("%.2f per CPU", s.LoadPerCPU)
	}
	defer func() {
		fmt.Fprintf(w, "  tune:          %s, %d sample(s) in 24h, last tune %s"+"\n", s.TuneMode, s.Samples24h, orNever(s.LastTune))
		fmt.Fprintf(w, "  runner config: %s (concurrent %d)"+"\n", s.RunnerConfig, s.Concurrent)
		for _, p := range s.Problems {
			fmt.Fprintln(w, "  PROBLEM:", p)
		}
	}()
	fmt.Fprintf(w, "runner-guard %s\n  docker:        %s (%dms)\n  disk:          %s\n  memory:        %.0f%%, load %s (over threshold %d run(s) in a row)\n  runner:        %s %s\n  blocked_by_us: %s\n  last_run:      %s\n  last_action:   %s\n",
		s.Version, docker, s.DockerMillis, s.Disk, s.MemoryPercent, load, s.PressureRuns, s.RunnerService, runner, blocked, s.LastRun, s.LastAction)
	return nil
}

func doUpdate(ctx context.Context, w io.Writer, checkOnly bool) (rerr error) {
	logf := func(format string, a ...any) { guard.Logf(w, time.Now(), format, a...) }
	cfg, err := config.Load(config.File())
	if errors.Is(err, os.ErrPermission) && checkOnly {
		cfg, err = config.Default(), nil // a non-root check only needs the default repo
	}
	if err != nil {
		return err
	}
	if !cfg.AutoUpdate && !checkOnly {
		logf("update skipped: auto_update is false")
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	if !checkOnly { // --check changes nothing, so it needs neither the lock nor root
		// Only the installed copy, in its admin-only folder, is ever replaced: a
		// copy in a folder others can write could be swapped between the
		// checksum check and the swap.
		if !samePath(bin, config.BinPath()) {
			return fmt.Errorf("update only replaces the installed %s; run that one, or reinstall with the install script", config.BinPath())
		}
		// Same lock as run: never swap the binary mid-run.
		unlock, err := waitLock(ctx, 30*time.Minute)
		if err != nil {
			return err
		}
		defer unlock()
		// Runs before unlock (defers are LIFO), so state.json is written under the lock.
		defer func() {
			noteUpdate(statePath(), rerr, func(msg string) error {
				if cfg.AlertWebhook == "" {
					return nil
				}
				return alert.Send(ctx, cfg.AlertWebhook, msg)
			})
		}()
		update.Cleanup(bin)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	rel, err := update.Check(ctx, client, "https://api.github.com", cfg.UpdateRepo)
	if errors.Is(err, update.ErrSkip) {
		logf("%v", err)
		return nil
	}
	if err != nil {
		return err
	}
	newer, err := update.Newer(update.Version, rel.Tag)
	if err != nil {
		return err
	}
	if !newer {
		logf("up to date (%s, latest %s)", update.Version, rel.Tag)
		return nil
	}
	if checkOnly {
		logf("update available: %s -> %s", update.Version, rel.Tag)
		return nil
	}
	verify := func(path string) error {
		r := execx.OS{}
		if _, err := r.Run(ctx, 60*time.Second, path, "selftest"); err != nil {
			return err
		}
		out, err := r.Run(ctx, 60*time.Second, path, "version")
		if err != nil {
			return err
		}
		if got := strings.TrimSpace(out); got != rel.Tag {
			return fmt.Errorf("new binary reports version %q, want %q", got, rel.Tag)
		}
		return nil
	}
	if err := update.Apply(ctx, client, rel, bin, verify); err != nil {
		return err
	}
	// Samples right after an update are not normal load; tuning skips them.
	if st, warn := state.Load(statePath()); warn == "" { // unreadable state: saving would wipe BlockedByUs
		st.LastUpdate = time.Now()
		state.Save(statePath(), st)
	}
	logf("updated %s -> %s", update.Version, rel.Tag)
	return nil
}

// noteUpdate counts self-updates that failed in a row (a bad signature, a
// broken download, a rollback). The third alerts: a fleet that silently stops
// updating also stops getting every later fix.
func noteUpdate(path string, uerr error, send func(string) error) {
	st, warn := state.Load(path)
	if warn != "" {
		return // unreadable state: saving would wipe BlockedByUs and the rest
	}
	if uerr == nil {
		if st.UpdateFailures == 0 {
			return
		}
		st.UpdateFailures, st.LastUpdateError = 0, ""
	} else {
		st.UpdateFailures++
		st.LastUpdateError = uerr.Error()
		if st.UpdateFailures == 3 {
			host, _ := os.Hostname()
			if err := send(fmt.Sprintf("[runner-guard] %s: self-update failed 3 times in a row: %v", host, uerr)); err != nil {
				fmt.Fprintln(os.Stderr, "[runner-guard] alert:", err)
			}
		}
	}
	state.Save(path, st)
}

func doInstall(ctx context.Context) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	bin := config.BinPath()
	for _, dir := range []string{filepath.Dir(bin), config.Dir(), filepath.Dir(config.File())} {
		// Only folders that are ours: on Linux the binary goes into the shared
		// /usr/local/bin, which some distros make group-writable (root:staff).
		ours := dir == config.Dir() || dir == filepath.Dir(config.File())
		if _, err := os.Stat(dir); err == nil && ours {
			if err := schedule.TrustedOwner(dir); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("%w (run as root/administrator)", err)
		}
		// Before writing anything there: see schedule.SecureDir.
		if err := schedule.SecureDir(ctx, execx.OS{}, dir); err != nil {
			return fmt.Errorf("secure %s: %w", dir, err)
		}
	}
	// A reinstall must not swap the binary or rewrite tasks under a running run.
	unlock, err := waitLock(ctx, 30*time.Minute)
	if err != nil {
		return err
	}
	defer unlock()
	if !samePath(src, bin) {
		if err := copyFile(src, bin); err != nil {
			return err
		}
	}
	if _, err := os.Stat(config.File()); os.IsNotExist(err) {
		b, _ := json.MarshalIndent(config.Default(), "", "  ")
		if err := fsx.WriteFile(config.File(), append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	if err := schedule.LockFile(ctx, execx.OS{}, config.File()); err != nil { // webhook URL is a secret
		return fmt.Errorf("lock %s: %w", config.File(), err)
	}
	if err := schedule.Install(ctx, execx.OS{}, bin); err != nil {
		return err
	}
	fmt.Printf("[runner-guard] installed %s\n  config: %s\n  state and log: %s\n", bin, config.File(), config.Dir())
	fmt.Println("  on a production host, set refuse_if_exists before the first scheduled run.")
	return nil
}

func samePath(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// copyFile writes dst via a temp file and a swap so a half copy never runs.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	os.Remove(tmp) // never reuse a file someone else created (and may still own)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = update.Replace(dst, tmp) // dst may be running: reinstall in place
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// selftest checks the pure decision logic; update runs it on a new binary
// before trusting it.
func selftest() error {
	var fails []string
	check := func(ok bool, what string) {
		if !ok {
			fails = append(fails, what)
		}
	}
	n, err := disk.ParseSize("1.5GB")
	check(err == nil && n == 1.5e9, "ParseSize 1.5GB")
	_, err = disk.ParseSize("12 bananas")
	check(err != nil, "ParseSize rejects garbage")
	check(disk.ShouldPrune(80, 75, false), "ShouldPrune 80/75")
	check(!disk.ShouldPrune(74.9, 75, false), "ShouldPrune 74.9/75")
	check(disk.ShouldPrune(10, 75, true), "ShouldPrune force")
	check(runnersvc.Decide(false, 0, 75, 90, true, false, "") == runnersvc.Block, "Decide docker dead -> block")
	check(runnersvc.Decide(true, 95, 75, 90, true, false, "") == runnersvc.Block, "Decide disk full -> block")
	check(runnersvc.Decide(true, 50, 75, 90, false, true, runnersvc.ReasonDisk) == runnersvc.Unblock, "Decide recovered -> unblock")
	check(runnersvc.Decide(true, 80, 75, 90, false, true, runnersvc.ReasonDisk) == runnersvc.None, "Decide hysteresis")
	check(runnersvc.Decide(true, 80, 75, 90, false, true, runnersvc.ReasonDocker) == runnersvc.Unblock, "Decide docker block lifts")
	check(runnersvc.Decide(true, 50, 75, 90, false, false, "") == runnersvc.None, "Decide leaves manual stop alone")
	up, err := update.Newer("v0.1.0", "v0.2.0")
	check(err == nil && up, "Newer up")
	down, err := update.Newer("v0.2.0", "v0.1.9")
	check(err == nil && !down, "Newer never downgrades")
	wsl, err := tune.SetWSL(nil, "memory", 8)
	check(err == nil && string(wsl) == "[wsl2]\nmemory=8GB\n", "SetWSL")
	check(len(tune.Decide(tune.Input{Now: time.Now(), Cores: 4}).Changes) == 0, "Decide without history changes nothing")
	if len(fails) > 0 {
		return fmt.Errorf("selftest failed: %s", strings.Join(fails, ", "))
	}
	return nil
}

func orNever(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

// statusTune fills the tuning part of status and lists setup problems that
// would quietly stop tuning or recovery from working.
func statusTune(ctx context.Context, d guard.Deps, st state.State, s *statusReport) {
	s.RunnerConfig = d.Cfg.RunnerConfig
	if n, err := tune.ReadConcurrent(d.Cfg.RunnerConfig); err == nil {
		s.Concurrent = n
	} else if s.RunnerExists {
		s.Problems = append(s.Problems, fmt.Sprintf("cannot read concurrent from %s; set runner_config (%v)", d.Cfg.RunnerConfig, err))
	}
	s.TuneMode = "auto"
	if !guard.TuneActive(d) {
		s.TuneMode = "report only"
	}
	if !st.LastTune.IsZero() {
		s.LastTune = st.LastTune.Format(time.RFC3339)
	}
	now := time.Now()
	h, _ := tune.Load(d.HistoryPath, now.Add(-24*time.Hour))
	s.Samples24h = len(h)
	if fileExists(config.BinPath()) && !st.LastRun.IsZero() {
		if last := lastSample(h); last.IsZero() || now.Sub(last) > 30*time.Minute {
			s.Problems = append(s.Problems, "no usage sample in the last 30 min; is the runner-guard-sample task/timer running?")
		}
	}
	for _, n := range guard.Knobs(&st) {
		if n.K.Frozen {
			s.Problems = append(s.Problems, n.Name+" tuning is frozen; check the log, then runner-guard tune --reset")
		}
	}
	if s.DockerHealthy && s.DiskPercent >= d.Cfg.BlockPercent {
		fix := "free space"
		if runtime.GOOS == "windows" {
			fix += " or raise desktop_budget_gb"
		}
		s.Problems = append(s.Problems, fmt.Sprintf("disk %.0f%% >= block_percent %g: run stops the runner until it falls below %g%%; %s", s.DiskPercent, d.Cfg.BlockPercent, guard.PrunePercent(d, st), fix))
	}
	if st.UpdateFailures >= 3 {
		s.Problems = append(s.Problems, fmt.Sprintf("self-update failed %d times in a row (last: %s); no later fixes arrive until it works", st.UpdateFailures, st.LastUpdateError))
	}
	if d.Cfg.RebootRecover {
		if err := guard.CanReboot(ctx, d); err != nil {
			s.Problems = append(s.Problems, "reboot_recover is on but "+err.Error())
		}
	}
	sort.Strings(s.Problems)
}

func lastSample(h []tune.Sample) time.Time {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Kind == "" {
			return h[i].T
		}
	}
	return time.Time{}
}
