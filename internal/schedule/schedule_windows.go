// Package schedule registers runner-guard with the OS scheduler.
package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

const timeout = 60 * time.Second

var now = time.Now // tests pin the clock

const sampleTask = "runner-guard-sample"

var tasks = []string{"runner-guard-run", "runner-guard-update", sampleTask, dockerctl.DesktopTask}

func currentUser() string { return os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME") }

// Install locks the install folder to Administrators/SYSTEM (Users may read
// and run only), then registers the tasks for the logged-on user. Docker
// Desktop lives in that user's session, so the tasks run only while they are
// logged on.
func Install(ctx context.Context, r execx.Runner, bin string) error {
	dir := filepath.Dir(bin)
	user := currentUser()
	// First run 15 min after install (time to set refuse_if_exists); the
	// daily update 30 min after an hourly run so they don't fight for the lock.
	runAt := now().Add(15 * time.Minute).Format("15:04")
	updateAt := now().Add(45 * time.Minute).Format("15:04")
	desktop := filepath.Join(os.Getenv("ProgramFiles"), "Docker", "Docker", "Docker Desktop.exe")
	cmds := [][]string{
		{"icacls", dir, "/inheritance:r", "/grant:r",
			"*S-1-5-32-544:(OI)(CI)F", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-545:(OI)(CI)RX"},
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-run", "/SC", "HOURLY", "/ST", runAt, "/TR", `"` + bin + `" run`},
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-update", "/SC", "DAILY", "/ST", updateAt, "/TR", `"` + bin + `" update`},
		// Not elevated on purpose: an elevated Docker Desktop behaves differently.
		{"schtasks", "/Create", "/F", "/IT", "/RU", user,
			"/TN", dockerctl.DesktopTask, "/SC", "ONCE", "/ST", "00:00", "/TR", `"` + desktop + `"`},
		sampleCmd(bin, user),
	}
	for _, c := range cmds {
		if _, err := r.Run(ctx, timeout, c[0], c[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func Uninstall(ctx context.Context, r execx.Runner) error {
	var errs []error
	for _, t := range tasks {
		if _, err := r.Run(ctx, timeout, "schtasks", "/Delete", "/F", "/TN", t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sampleCmd: every 5 minutes, through a headless console so no window
// flashes up on the desktop.
func sampleCmd(bin, user string) []string {
	return []string{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
		"/TN", sampleTask, "/SC", "MINUTE", "/MO", "5", "/TR", `conhost.exe --headless "` + bin + `" sample`}
}

// EnsureSample adds the sample task on hosts installed before it existed.
func EnsureSample(ctx context.Context, r execx.Runner, bin string) (bool, error) {
	if _, err := r.Run(ctx, timeout, "schtasks", "/Query", "/TN", sampleTask); err == nil {
		return false, nil
	}
	c := sampleCmd(bin, currentUser())
	_, err := r.Run(ctx, timeout, c[0], c[1:]...)
	return err == nil, err
}
