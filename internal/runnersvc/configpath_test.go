package runnersvc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestFlagValue(t *testing.T) {
	win := `"C:\Runner X\gitlab-runner.exe" run --working-directory "C:\Runner X" --config "C:\Runner X\config.toml" --service "gitlab-runner" --syslog`
	if got := flagValue(win, "--config", "-c"); got != `C:\Runner X\config.toml` {
		t.Fatal(got)
	}
	if got := firstArg(win); got != `C:\Runner X\gitlab-runner.exe` {
		t.Fatal(got)
	}
	lin := "/usr/bin/gitlab-runner run --working-directory /home/gitlab-runner --config=/srv/gl/config.toml --user gitlab-runner ; ignore_errors=no ; start_time=[n/a] }"
	if got := flagValue(lin, "--config", "-c"); got != "/srv/gl/config.toml" {
		t.Fatal(got)
	}
}

func TestConfigPathFallsBackToServiceFlag(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "config.toml")
	os.WriteFile(real, []byte("concurrent = 1\n"), 0o600)
	cmd := `"` + filepath.Join(dir, "gitlab-runner") + `" run --config "` + real + `"`
	out := "argv[]=" + cmd + " ; ignore_errors=no }" // systemctl show
	if runtime.GOOS == "windows" {
		out = "        BINARY_PATH_NAME   : " + cmd + "\r\n" // sc.exe qc
	}
	f := &execx.Fake{Default: execx.FakeResponse{Out: out}}
	for _, configured := range []string{"", filepath.Join(dir, "missing.toml")} {
		if got := ConfigPath(context.Background(), f, "gitlab-runner", configured); got != real {
			t.Fatalf("configured %q: got %q", configured, got)
		}
	}
	if got := ConfigPath(context.Background(), f, "gitlab-runner", real); got != real || len(f.Calls) != 2 {
		t.Fatalf("an existing configured path must win without asking the service: %q %v", got, f.Calls)
	}
}
