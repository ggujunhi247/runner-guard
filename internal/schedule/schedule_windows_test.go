package schedule

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

func TestInstallWindowsCommands(t *testing.T) {
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
			"/TN", "runner-guard-run", "/SC", "HOURLY", "/TR", `"` + bin + `" run`},
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-update", "/SC", "DAILY", "/TR", `"` + bin + `" update`},
		{"schtasks", "/Create", "/F", "/IT", "/RU", user,
			"/TN", "runner-guard-desktop", "/SC", "ONCE", "/ST", "00:00", "/TR", `"` + desktop + `"`},
	}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls\n%q\nwant\n%q", f.Calls, want)
	}
}
