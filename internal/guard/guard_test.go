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

	"github.com/ggujunhi/runner-guard/internal/config"
	"github.com/ggujunhi/runner-guard/internal/execx"
	"github.com/ggujunhi/runner-guard/internal/runnersvc"
	"github.com/ggujunhi/runner-guard/internal/state"
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
		Exists: func(string) bool { return false },
		Usage:  func(context.Context) (float64, string, error) { return pct, "test", nil },
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
