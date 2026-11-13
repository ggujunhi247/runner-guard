package schedule

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

func TestInstallLinuxUnits(t *testing.T) {
	unitDir = t.TempDir()
	f := &execx.Fake{}
	if err := Install(context.Background(), f, "/usr/local/bin/runner-guard"); err != nil {
		t.Fatal(err)
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(unitDir, n)); return string(b) }
	for _, want := range []string{"OnBootSec=5min", "OnUnitActiveSec=1h", "Persistent=true"} {
		if !strings.Contains(read("runner-guard-run.timer"), want) {
			t.Errorf("run timer lacks %s", want)
		}
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
	if !reflect.DeepEqual(last, []string{"systemctl", "enable", "--now", "runner-guard-run.timer", "runner-guard-update.timer"}) {
		t.Fatalf("last call %v", last)
	}
}
