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
		{"icacls", `C:\ProgramData\runner-guard`, "/inheritance:r", "/grant:r",
			"*S-1-5-32-544:(OI)(CI)F", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-545:(OI)(CI)RX"},
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
