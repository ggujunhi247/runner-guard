package guard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/pressure"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/state"
)

var now = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

type svc int

const (
	svcRunning svc = iota
	svcStopped
	svcMissing
)

// responses covers both the Linux and Windows service commands so the same
// test runs on either CI OS.
func responses(dockerUp bool, s svc) map[string]execx.FakeResponse {
	m := map[string]execx.FakeResponse{}
	if dockerUp {
		m["docker info --format {{.ServerVersion}}"] = execx.FakeResponse{Out: "27.0.0\n"}
	} else {
		m["docker info --format {{.ServerVersion}}"] = execx.FakeResponse{Err: errors.New("cannot connect")}
	}
	switch s {
	case svcRunning:
		m["systemctl is-active gitlab-runner"] = execx.FakeResponse{Out: "active\n"}
		m["sc.exe query gitlab-runner"] = execx.FakeResponse{Out: "STATE : 4  RUNNING"}
	case svcStopped:
		m["systemctl is-active gitlab-runner"] = execx.FakeResponse{Out: "inactive\n", Err: errors.New("exit status 3")}
		m["sc.exe query gitlab-runner"] = execx.FakeResponse{Out: "STATE : 1  STOPPED"}
	case svcMissing:
		m["systemctl cat gitlab-runner"] = execx.FakeResponse{Err: errors.New("no such unit")}
		m["sc.exe query gitlab-runner"] = execx.FakeResponse{Out: "OpenService FAILED 1060:", Err: errors.New("exit status 1060")}
	}
	return m
}

func newDeps(t *testing.T, f *execx.Fake, pct float64) Deps {
	dir := t.TempDir()
	return Deps{
		R: f, Cfg: config.Default(),
		StatePath: filepath.Join(dir, "state.json"), LockPath: filepath.Join(dir, "lock"),
		Now: func() time.Time { return now }, Log: &bytes.Buffer{},
		Exists:   func(string) bool { return false },
		Usage:    func(context.Context) (float64, string, error) { return pct, "test", nil },
		Pressure: func() (pressure.Sample, error) { return pressure.Sample{MemPct: 50, LoadPerCPU: -1}, nil },
	}
}

func called(f *execx.Fake, word string) bool {
	for _, c := range f.Calls {
		for _, a := range c[1:] {
			if a == word {
				return true
			}
		}
	}
	return false
}

func TestRefusesOnMarker(t *testing.T) {
	f := &execx.Fake{}
	d := newDeps(t, f, 50)
	d.Cfg.RefuseIfExists = []string{"/etc/prod-marker"}
	d.Exists = func(p string) bool { return p == "/etc/prod-marker" }
	if _, err := Run(context.Background(), d, false); err == nil {
		t.Fatal("want refusal")
	}
	if len(f.Calls) != 0 {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestDockerMissingDoesNotBlock(t *testing.T) {
	f := &execx.Fake{Default: execx.FakeResponse{Err: execx.ErrNotFound}}
	d := newDeps(t, f, 50)
	if _, err := Run(context.Background(), d, false); err == nil {
		t.Fatal("want error")
	}
	if called(f, "stop") {
		t.Fatalf("stopped runner: %v", f.Calls)
	}
}

func TestDockerDeadAndCapReachedBlocksWithoutPrune(t *testing.T) {
	f := &execx.Fake{Responses: responses(false, svcRunning)}
	d := newDeps(t, f, 50)
	state.Save(d.StatePath, state.State{DockerRestarts: []time.Time{
		now.Add(-time.Hour), now.Add(-2 * time.Hour), now.Add(-3 * time.Hour)}})
	rep, err := Run(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	if called(f, "prune") || called(f, "restart") {
		t.Fatalf("unexpected calls %v", f.Calls)
	}
	if !called(f, "stop") || rep.Action != runnersvc.Block {
		t.Fatalf("not blocked: %v %v", rep.Action, f.Calls)
	}
	if s, _ := state.Load(d.StatePath); !s.BlockedByUs {
		t.Fatal("BlockedByUs not saved")
	}
}

func TestDockerDeadRestartsAndRecovers(t *testing.T) {
	f := &execx.Fake{Responses: responses(false, svcRunning)}
	d := newDeps(t, f, 50)
	d.Sleep = func(time.Duration) { // engine comes back after the restart
		f.Responses["docker info --format {{.ServerVersion}}"] = execx.FakeResponse{Out: "27.0.0"}
	}
	rep, err := Run(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Restarted || !rep.DockerHealthy || called(f, "stop") {
		t.Fatalf("rep %+v calls %v", rep, f.Calls)
	}
	if s, _ := state.Load(d.StatePath); len(s.DockerRestarts) != 1 {
		t.Fatalf("restart not recorded: %+v", s)
	}
}

func TestRecoveryUnblocks(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcStopped)}
	d := newDeps(t, f, 50)
	state.Save(d.StatePath, state.State{BlockedByUs: true})
	rep, err := Run(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	if !called(f, "start") || rep.Action != runnersvc.Unblock {
		t.Fatalf("not unblocked: %v %v", rep.Action, f.Calls)
	}
	if s, _ := state.Load(d.StatePath); s.BlockedByUs {
		t.Fatal("flag not cleared")
	}
}

func TestMissingServiceWarnsAndContinues(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcMissing)}
	d := newDeps(t, f, 80)
	rep, err := Run(context.Background(), d, false)
	if err != nil {
		t.Fatal(err)
	}
	if !called(f, "prune") || !rep.Pruned {
		t.Fatalf("prune skipped: %v", f.Calls)
	}
	if len(rep.Warnings) == 0 || !strings.Contains(strings.Join(rep.Warnings, ";"), "not found") {
		t.Fatalf("warnings %v", rep.Warnings)
	}
}

func TestLockHeldReturnsErrLocked(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcRunning)}
	d := newDeps(t, f, 50)
	os.WriteFile(d.LockPath, []byte("1"), 0o644)
	if _, err := Run(context.Background(), d, false); !errors.Is(err, state.ErrLocked) {
		t.Fatalf("got %v", err)
	}
}

func TestCorruptStateContinues(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcRunning)}
	d := newDeps(t, f, 50)
	os.WriteFile(d.StatePath, []byte("{not json"), 0o644)
	rep, err := Run(context.Background(), d, false)
	if err != nil || len(rep.Warnings) == 0 {
		t.Fatalf("rep %+v err %v", rep, err)
	}
}

func TestOpenLogRotates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "runner-guard.log"), bytes.Repeat([]byte("x"), 1<<20), 0o644)
	w, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("new\n"))
	w.Close()
	if fi, err := os.Stat(filepath.Join(dir, "runner-guard.log.1")); err != nil || fi.Size() != 1<<20 {
		t.Fatalf("rotated file: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "runner-guard.log")); string(b) != "new\n" {
		t.Fatalf("log %q", b)
	}
}

func TestDockerBlockLiftsWhenDockerBack(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcStopped)}
	d := newDeps(t, f, 80) // above prune, below block
	state.Save(d.StatePath, state.State{BlockedByUs: true, BlockReason: runnersvc.ReasonDocker})
	rep, err := Run(context.Background(), d, false)
	if err != nil || rep.Action != runnersvc.Unblock || !called(f, "start") {
		t.Fatalf("rep %+v err %v calls %v", rep, err, f.Calls)
	}
}

func TestStopFailsAndStillRunningClearsFlag(t *testing.T) {
	r := responses(true, svcRunning)
	r["systemctl stop gitlab-runner"] = execx.FakeResponse{Err: errors.New("denied")}
	r["sc.exe stop gitlab-runner"] = execx.FakeResponse{Err: errors.New("denied")}
	f := &execx.Fake{Responses: r}
	d := newDeps(t, f, 95)
	if _, err := Run(context.Background(), d, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := state.Load(d.StatePath); s.BlockedByUs {
		t.Fatal("flag kept although the runner is still running")
	}
}

func TestStopTimesOutButServiceStoppedKeepsFlag(t *testing.T) {
	r := responses(true, svcRunning)
	r["systemctl stop gitlab-runner"] = execx.FakeResponse{Err: errors.New("timed out")}
	r["sc.exe stop gitlab-runner"] = execx.FakeResponse{Err: errors.New("timed out")}
	f := &execx.Fake{Responses: r}
	d := newDeps(t, f, 95)
	// After the failed stop, status reports the service gone quiet.
	d.R = &switchingFake{Fake: f, after: "stop", then: responses(true, svcStopped)}
	if _, err := Run(context.Background(), d, false); err != nil {
		t.Fatal(err)
	}
	if s, _ := state.Load(d.StatePath); !s.BlockedByUs || s.BlockReason != runnersvc.ReasonDisk {
		t.Fatalf("flag lost: %+v", s)
	}
}

func TestReclaimAfterDiskBlockThenUnblocks(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcRunning)}
	d := newDeps(t, f, 95)
	d.Cfg.DesktopReclaim = true
	reclaimed := false
	d.Usage = func(context.Context) (float64, string, error) {
		if reclaimed {
			return 50, "after", nil
		}
		return 95, "before", nil
	}
	d.Reclaim = func(context.Context) (string, error) { reclaimed = true; return "compacted", nil }
	var alerts []string
	d.Alert = func(_ context.Context, msg string) error { alerts = append(alerts, msg); return nil }
	rep, err := Run(context.Background(), d, false)
	if err != nil || !reclaimed || rep.Action != runnersvc.Unblock {
		t.Fatalf("rep %+v err %v", rep, err)
	}
	s, _ := state.Load(d.StatePath)
	if s.BlockedByUs || !s.LastReclaim.Equal(now) {
		t.Fatalf("state %+v", s)
	}
	if len(alerts) != 1 || !strings.Contains(alerts[0], "stopped") || !strings.Contains(alerts[0], "compacted") || !strings.Contains(alerts[0], "started") {
		t.Fatalf("alerts %q", alerts)
	}
}

func TestReclaimAtMostDaily(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcStopped)}
	d := newDeps(t, f, 95)
	d.Cfg.DesktopReclaim = true
	state.Save(d.StatePath, state.State{BlockedByUs: true, BlockReason: runnersvc.ReasonDisk, LastReclaim: now.Add(-time.Hour)})
	d.Reclaim = func(context.Context) (string, error) { t.Fatal("reclaimed twice in a day"); return "", nil }
	if _, err := Run(context.Background(), d, false); err != nil {
		t.Fatal(err)
	}
}

func TestPruneAloneDoesNotAlert(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcRunning)}
	d := newDeps(t, f, 80)
	d.Alert = func(context.Context, string) error { t.Fatal("alerted on prune"); return nil }
	if _, err := Run(context.Background(), d, false); err != nil {
		t.Fatal(err)
	}
}

// switchingFake swaps in new responses after a call containing word.
type switchingFake struct {
	*execx.Fake
	after string
	then  map[string]execx.FakeResponse
}

func (s *switchingFake) Run(ctx context.Context, to time.Duration, name string, args ...string) (string, error) {
	out, err := s.Fake.Run(ctx, to, name, args...)
	for _, a := range args {
		if a == s.after {
			for k, v := range s.then {
				s.Fake.Responses[k] = v
			}
		}
	}
	return out, err
}

func TestStuckContainersRemovedWhenEnabled(t *testing.T) {
	r := responses(true, svcRunning)
	r["docker ps -q --no-trunc --filter label=com.gitlab.gitlab-runner.managed=true --filter status=running"] = execx.FakeResponse{Out: "dead\n"}
	r["docker inspect --format {{.Id}} {{.State.StartedAt}} dead"] = execx.FakeResponse{Out: "dead 2026-11-13T01:00:00Z\n"}
	f := &execx.Fake{Responses: r}
	d := newDeps(t, f, 50)
	d.Cfg.StuckContainerAge = "" // explicitly off
	if _, err := Run(context.Background(), d, false); err != nil {
		t.Fatal(err)
	}
	if called(f, "rm") {
		t.Fatal("removed containers although stuck_container_age is off")
	}
	d.Cfg = config.Default() // on by default (6h)
	var alerts []string
	d.Alert = func(_ context.Context, m string) error { alerts = append(alerts, m); return nil }
	rep, err := Run(context.Background(), d, false)
	if err != nil || len(rep.Stuck) != 1 || !called(f, "rm") {
		t.Fatalf("rep %+v err %v", rep, err)
	}
	if len(alerts) != 1 || !strings.Contains(alerts[0], "running longer than 6h") {
		t.Fatalf("alerts %q", alerts)
	}
}

func TestPressureAlertsOnceWhenSustained(t *testing.T) {
	f := &execx.Fake{Responses: responses(true, svcRunning)}
	d := newDeps(t, f, 50)
	d.Pressure = func() (pressure.Sample, error) { return pressure.Sample{MemPct: 97, LoadPerCPU: -1}, nil }
	var alerts []string
	d.Alert = func(_ context.Context, m string) error { alerts = append(alerts, m); return nil }
	for i := 0; i < 5; i++ {
		if _, err := Run(context.Background(), d, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(alerts) != 1 || !strings.Contains(alerts[0], "memory 97%") {
		t.Fatalf("want exactly one alert on run 3, got %q", alerts)
	}
	if called(f, "stop") {
		t.Fatal("pressure must never stop the runner")
	}
	d.Pressure = func() (pressure.Sample, error) { return pressure.Sample{MemPct: 40, LoadPerCPU: -1}, nil }
	Run(context.Background(), d, false)
	if s, _ := state.Load(d.StatePath); s.PressureRuns != 0 {
		t.Fatalf("streak not reset: %d", s.PressureRuns)
	}
}
