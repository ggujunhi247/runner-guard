package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/state"
)

func TestApplyWSLWithDockerDeadRestoresAndKeepsRunnerBlocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	orig := []byte("[wsl2]\nmemory=8GB\n")
	os.WriteFile(filepath.Join(home, ".wslconfig"), orig, 0o644)
	f := &execx.Fake{Responses: responses(false, svcRunning)} // Docker never answers
	d := newDeps(t, f, 50)
	d.HistoryPath = filepath.Join(filepath.Dir(d.StatePath), "history.jsonl")
	st := state.State{}
	if err := applyWSL(context.Background(), d, &st, "memory", 10, now); err == nil {
		t.Fatal("no error with Docker dead")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".wslconfig")); string(b) != string(orig) {
		t.Fatalf(".wslconfig not restored: %q", b)
	}
	if !called(f, "stop") || called(f, "start") || !st.BlockedByUs || st.BlockReason != runnersvc.ReasonTune {
		t.Fatalf("runner must stay stopped and flagged until Docker answers: %+v %v", st, f.Calls)
	}
	for _, c := range f.Calls {
		if c[0] == "wsl" {
			t.Fatalf("wsl --shutdown before Docker Desktop was stopped: %v", f.Calls)
		}
		if c[0] == "docker" && len(c) > 2 && c[2] == "stop" {
			break
		}
	}
}

// A crash between writing .wslconfig and verifying it: the next run puts the
// old file back, restarts WSL and freezes that knob.
func TestInterruptedWSLChangeIsUndone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".wslconfig")
	os.WriteFile(path, []byte("[wsl2]\nmemory=12GB\n"), 0o644) // the unverified change
	f := &execx.Fake{Responses: responses(true, svcRunning)}
	d := newDeps(t, f, 50)
	d.HistoryPath = filepath.Join(filepath.Dir(d.StatePath), "history.jsonl")
	os.WriteFile(lastGood(d), []byte("[wsl2]\nmemory=10GB\n"), 0o644)
	state.Save(d.StatePath, state.State{PendingWSLKey: "memory", PendingWSLPath: path, PendingWSLExisted: true})
	var alerts []string
	d.Alert = func(_ context.Context, msg string) error { alerts = append(alerts, msg); return nil }
	Run(context.Background(), d, false)
	if b, _ := os.ReadFile(path); string(b) != "[wsl2]\nmemory=10GB\n" {
		t.Fatalf(".wslconfig not restored: %q", b)
	}
	st, _ := state.Load(d.StatePath)
	if st.PendingWSLPath != "" || !st.WSLMemoryGB.Frozen || !called(f, "--shutdown") {
		t.Fatalf("state %+v calls %v", st, f.Calls)
	}
	if !strings.Contains(strings.Join(alerts, ";"), "interrupted") {
		t.Fatalf("not alerted: %v", alerts)
	}
}
