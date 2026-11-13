package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/guard"
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
	for _, k := range []string{"version", "docker_healthy", "disk_percent", "runner_running", "blocked_by_us", "last_action"} {
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
