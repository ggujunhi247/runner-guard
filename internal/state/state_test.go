package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "state.json")
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s := State{DockerRestarts: []time.Time{now}, BlockedByUs: true, LastAction: "x", LastRun: now}
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	got, warn := Load(p)
	if warn != "" || !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v warn %q", got, warn)
	}
}

func TestLoadCorruptStateWarns(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"corrupt": "{not json", "empty": ""} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		got, warn := Load(p)
		if warn == "" || !reflect.DeepEqual(got, State{}) {
			t.Fatalf("%s: got %+v warn %q", name, got, warn)
		}
	}
	if _, warn := Load(filepath.Join(dir, "missing")); warn != "" {
		t.Fatalf("missing file should not warn: %q", warn)
	}
}

func TestLockExcludesSecondHolder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	unlock, err := Lock(p, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(p, 2*time.Hour); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	unlock2, err := Lock(p, 2*time.Hour)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock2()
}

func TestLockReplacesStale(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	os.WriteFile(p, []byte("1\n"), 0o644)
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(p, old, old)
	unlock, err := Lock(p, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
