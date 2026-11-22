package doctor

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/guard"
)

// readOnly are the only commands doctor may run: queries. Anything that
// restarts, stops, deletes, creates or reboots fails this test.
func readOnly(c []string) bool {
	j := strings.Join(c, " ")
	for _, ok := range []string{
		"docker info", "docker ps", "docker version",
		"sc.exe query ", "sc.exe qc ", "systemctl is-active ", "systemctl is-enabled ", "systemctl cat ", "systemctl show ",
		"wsl -d docker-desktop -u root cat /proc/meminfo /proc/vmstat",
		"reg query ", "schtasks /Query ",
	} {
		if strings.HasPrefix(j, ok) {
			return true
		}
	}
	return false
}

func TestDoctorOnlyReads(t *testing.T) {
	f := &execx.Fake{}
	cfg := config.Default()
	cfg.RunnerConfig = filepath.Join(t.TempDir(), "missing.toml")
	cfg.RebootRecover = true
	d := guard.Deps{R: f, Cfg: cfg, Now: time.Now}
	cs := Run(context.Background(), d)
	if len(cs) < 5 {
		t.Fatalf("only %d checks", len(cs))
	}
	for _, c := range f.Calls {
		if !readOnly(c) {
			t.Errorf("doctor ran a command that is not a query: %q", c)
		}
	}
	var buf bytes.Buffer
	Print(&buf, cs)
	if !strings.Contains(buf.String(), "concurrent readable") {
		t.Fatalf("%s", buf.String())
	}
}
