package schedule

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestInstallLinuxUnits(t *testing.T) {
	unitDir = t.TempDir()
	f := &execx.Fake{}
	if err := Install(context.Background(), f, "/usr/local/bin/runner-guard"); err != nil {
		t.Fatal(err)
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(unitDir, n)); return string(b) }
	for _, want := range []string{"OnActiveSec=15min", "OnUnitActiveSec=1h", "Persistent=true"} {
		if !strings.Contains(read("runner-guard-run.timer"), want) {
			t.Errorf("run timer lacks %s", want)
		}
	}
	if !strings.Contains(read("runner-guard-sample.service"), "ExecStart=/usr/local/bin/runner-guard sample") {
		t.Error("sample service")
	}
	for _, want := range []string{"OnCalendar=daily", "RandomizedDelaySec=1h"} {
		if !strings.Contains(read("runner-guard-update.timer"), want) {
			t.Errorf("update timer lacks %s", want)
		}
	}
	svc := read("runner-guard-run.service")
	for _, want := range []string{"Type=oneshot", "Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "ExecStart=/usr/local/bin/runner-guard run"} {
		if !strings.Contains(svc, want) {
			t.Errorf("run service lacks %s", want)
		}
	}
	last := f.Calls[len(f.Calls)-1]
	if !reflect.DeepEqual(last, []string{"systemctl", "enable", "--now", "runner-guard-run.timer", "runner-guard-update.timer", "runner-guard-sample.timer"}) {
		t.Fatalf("last call %v", last)
	}
}

func TestEnsureSampleOnlyWhenMissing(t *testing.T) {
	unitDir = t.TempDir()
	f := &execx.Fake{}
	if added, err := EnsureSample(context.Background(), f, "/usr/local/bin/runner-guard"); !added || err != nil {
		t.Fatal(added, err)
	}
	if added, _ := EnsureSample(context.Background(), f, "/usr/local/bin/runner-guard"); added {
		t.Fatal("added twice")
	}
}

func TestTrustedOwnerRefusesWritableByOthers(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o777)
	if TrustedOwner(dir) == nil {
		t.Fatal("a world-writable folder was trusted")
	}
}
