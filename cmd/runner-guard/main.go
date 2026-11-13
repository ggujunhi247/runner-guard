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
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/alert"
	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/disk"
	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/guard"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/schedule"
	"github.com/ggujunhi247/runner-guard/internal/state"
	"github.com/ggujunhi247/runner-guard/internal/update"
)

const usage = `usage: runner-guard <command>

  check | status [--json]  show disk, Docker and runner state; change nothing
  run [--force]            prune, repair Docker, block/unblock the runner
  update [--check]         install the latest release (checksum verified)
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
	if err := fs.Parse(args[1:]); err != nil {
		return 2
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
		if d, err = deps(os.Stdout); err == nil {
			err = runStatus(ctx, d, *asJSON, os.Stdout)
		}
	case "run":
		err = withLog(func(w io.Writer) error {
			d, err := deps(w)
			if err != nil {
				return err
			}
			_, err = guard.Run(ctx, d, *force)
			if err != nil && !errors.Is(err, state.ErrLocked) && d.Alert != nil {
				host, _ := os.Hostname()
				d.Alert(ctx, fmt.Sprintf("[runner-guard] %s: run failed: %v", host, err))
			}
			return err
		})
	case "update":
		err = withLog(func(w io.Writer) error { return doUpdate(ctx, w, *checkOnly) })
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

func deps(log io.Writer) (guard.Deps, error) {
	cfg, err := config.Load(config.File())
	if err != nil {
		return guard.Deps{}, err
	}
	d := guard.Deps{
		R: execx.OS{}, Cfg: cfg,
		StatePath: statePath(), LockPath: lockPath(),
		Now: time.Now, Log: log, Sleep: time.Sleep,
	}
	if cfg.AlertWebhook != "" {
		d.Alert = func(ctx context.Context, msg string) error { return alert.Send(ctx, cfg.AlertWebhook, msg) }
	}
	return d, nil
}

func statePath() string { return filepath.Join(config.Dir(), "state.json") }
func lockPath() string  { return filepath.Join(config.Dir(), "lock") }

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
	Version       string  `json:"version"`
	DockerHealthy bool    `json:"docker_healthy"`
	DiskPercent   float64 `json:"disk_percent"`
	Disk          string  `json:"disk"`
	RunnerService string  `json:"runner_service"`
	RunnerExists  bool    `json:"runner_exists"`
	RunnerRunning bool    `json:"runner_running"`
	BlockedByUs   bool    `json:"blocked_by_us"`
	BlockReason   string  `json:"block_reason"`
	LastRun       string  `json:"last_run"`
	LastAction    string  `json:"last_action"`
}

func runStatus(ctx context.Context, d guard.Deps, asJSON bool, w io.Writer) error {
	s := statusReport{Version: update.Version, RunnerService: d.Cfg.RunnerService}
	s.DockerHealthy = dockerctl.Healthy(ctx, d.R)
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
	s.BlockedByUs, s.BlockReason, s.LastAction = st.BlockedByUs, st.BlockReason, st.LastAction
	if !st.LastRun.IsZero() {
		s.LastRun = st.LastRun.Format(time.RFC3339)
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	docker := "NOT responding"
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
	fmt.Fprintf(w, "runner-guard %s\n  docker:        %s\n  disk:          %s\n  runner:        %s %s\n  blocked_by_us: %s\n  last_run:      %s\n  last_action:   %s\n",
		s.Version, docker, s.Disk, s.RunnerService, runner, blocked, s.LastRun, s.LastAction)
	return nil
}

func doUpdate(ctx context.Context, w io.Writer, checkOnly bool) error {
	logf := func(format string, a ...any) { guard.Logf(w, time.Now(), format, a...) }
	cfg, err := config.Load(config.File())
	if err != nil {
		return err
	}
	if !cfg.AutoUpdate && !checkOnly {
		logf("update skipped: auto_update is false")
		return nil
	}
	// Same lock as run: never swap the binary mid-run.
	unlock, err := waitLock(ctx, 30*time.Minute)
	if err != nil {
		return err
	}
	defer unlock()
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	update.Cleanup(bin)

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
	logf("updated %s -> %s", update.Version, rel.Tag)
	return nil
}

func doInstall(ctx context.Context) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	bin := config.BinPath()
	for _, dir := range []string{filepath.Dir(bin), config.Dir(), filepath.Dir(config.File())} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("%w (run as root/administrator)", err)
		}
	}
	if !samePath(src, bin) {
		if err := copyFile(src, bin); err != nil {
			return err
		}
	}
	if _, err := os.Stat(config.File()); os.IsNotExist(err) {
		b, _ := json.MarshalIndent(config.Default(), "", "  ")
		if err := os.WriteFile(config.File(), append(b, '\n'), 0o644); err != nil {
			return err
		}
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

// copyFile writes dst via a temp file and rename so a half copy never runs.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
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
	if len(fails) > 0 {
		return fmt.Errorf("selftest failed: %s", strings.Join(fails, ", "))
	}
	return nil
}
