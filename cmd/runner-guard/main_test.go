package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/guard"
	"github.com/ggujunhi247/runner-guard/internal/state"
)

func TestStatusJSON(t *testing.T) {
	dir := t.TempDir()
	d := guard.Deps{
		R: &execx.Fake{}, Cfg: config.Default(),
		StatePath: filepath.Join(dir, "state.json"), LockPath: filepath.Join(dir, "lock"),
		Now: time.Now, Log: &bytes.Buffer{},
		Usage: func(context.Context) (float64, string, error) { return 42, "test", nil },
	}
	var out bytes.Buffer
	if err := runStatus(context.Background(), d, true, &out); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	for _, k := range []string{"version", "docker_healthy", "disk_percent", "runner_running", "blocked_by_us", "last_action", "docker_latency_ms", "memory_percent", "load_per_cpu"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %s in %s", k, out.String())
		}
	}
}

func TestSelftest(t *testing.T) {
	if err := selftest(); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsRootOnlyForWritingCommands(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("only meaningful for a non-root Linux user")
	}
	for _, c := range []struct {
		cmd          string
		check, reset bool
		want         bool
	}{{"run", false, false, true}, {"sample", false, false, true}, {"update", false, false, true},
		{"update", true, false, false}, {"status", false, false, false}, {"tune", false, false, false},
		{"tune", false, true, true}, {"uninstall", false, false, true}} {
		if got := needsRoot(c.cmd, c.check, c.reset) != ""; got != c.want {
			t.Errorf("%s check=%v reset=%v: needs root %v, want %v", c.cmd, c.check, c.reset, got, c.want)
		}
	}
}

func TestUpdateFailuresAlertOnTheThird(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	var sent []string
	send := func(m string) error { sent = append(sent, m); return nil }
	for i := 0; i < 4; i++ {
		noteUpdate(p, errors.New("checksums.txt signature does not match"), send)
	}
	st, _ := state.Load(p)
	if st.UpdateFailures != 4 || len(sent) != 1 || !strings.Contains(sent[0], "3 times") {
		t.Fatalf("failures %d, alerts %q", st.UpdateFailures, sent)
	}
	noteUpdate(p, nil, send)
	if st, _ := state.Load(p); st.UpdateFailures != 0 || st.LastUpdateError != "" {
		t.Fatalf("not reset: %+v", st)
	}
}
