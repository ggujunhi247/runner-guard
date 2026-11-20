package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestHeadless(t *testing.T) {
	if got := headless(`C:\pg.exe`, "run"); got != `conhost.exe --headless "C:\pg.exe" run` {
		t.Fatal(got)
	}
}

func TestInstallWindowsCommands(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 11, 14, 9, 50, 0, 0, time.Local) }
	f := &execx.Fake{}
	bin := `C:\ProgramData\runner-guard\runner-guard.exe`
	if err := Install(context.Background(), f, bin); err != nil {
		t.Fatal(err)
	}
	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	desktop := filepath.Join(os.Getenv("ProgramFiles"), "Docker", "Docker", "Docker Desktop.exe")
	want := [][]string{
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-run", "/SC", "HOURLY", "/ST", "10:05", "/TR", headless(bin, "run")},
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-update", "/SC", "DAILY", "/ST", "10:35", "/TR", headless(bin, "update")},
		{"schtasks", "/Create", "/F", "/IT", "/RU", user,
			"/TN", "runner-guard-desktop", "/SC", "ONCE", "/ST", "00:00", "/TR", `"` + desktop + `"`},
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-sample", "/SC", "MINUTE", "/MO", "5", "/TR", `conhost.exe --headless "` + bin + `" sample`},
	}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls\n%q\nwant\n%q", f.Calls, want)
	}
}

func TestEnsureSampleOnlyWhenMissing(t *testing.T) {
	f := &execx.Fake{}
	if added, err := EnsureSample(context.Background(), f, `C:\x.exe`); added || err != nil || len(f.Calls) != 1 {
		t.Fatalf("existing task touched: %v %v %v", added, err, f.Calls)
	}
	f = &execx.Fake{Responses: map[string]execx.FakeResponse{"schtasks /Query /TN runner-guard-sample": {Err: errors.New("not found")}}}
	if added, err := EnsureSample(context.Background(), f, `C:\x.exe`); !added || err != nil || len(f.Calls) != 2 {
		t.Fatalf("missing task not added: %v %v %v", added, err, f.Calls)
	}
}

func TestSecureDirTakesOwnershipBeforeGranting(t *testing.T) {
	f := &execx.Fake{}
	if err := SecureDir(context.Background(), f, `C:\ProgramData\runner-guard`); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 3 || f.Calls[0][2] != "/setowner" || f.Calls[1][2] != "/inheritance:r" || f.Calls[2][2] != "/reset" {
		t.Fatalf("calls %q", f.Calls)
	}
}
