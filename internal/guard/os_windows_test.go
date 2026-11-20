package guard

import (
	"context"
	"os"
	"path/filepath"
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
