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

// SecureDir makes Administrators own dir and everything in it, and lets only
// Administrators/SYSTEM write (Users read and run). It runs before anything is
// written there: Users may create folders in ProgramData, and an owner can
// always grant themselves access again, so a folder (or a planted *.new) a
// user made first would let them swap the exe the elevated tasks run.
func SecureDir(ctx context.Context, r execx.Runner, dir string) error {
	cmds := [][]string{
		{"icacls", dir, "/setowner", "*S-1-5-32-544", "/T", "/C", "/Q"},
		{"icacls", dir, "/inheritance:r", "/grant:r",
			"*S-1-5-32-544:(OI)(CI)F", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-545:(OI)(CI)RX"},
	}
	for _, c := range cmds {
		if _, err := r.Run(ctx, timeout, c[0], c[1:]...); err != nil {
			return err
		}
	}
	// Drop any explicit grants on files already there; fails harmlessly when empty.
	r.Run(ctx, timeout, "icacls", filepath.Join(dir, "*"), "/reset", "/T", "/C", "/Q")
	return nil
}

// LockFile lets only Administrators/SYSTEM read path (the config holds the webhook URL).
func LockFile(ctx context.Context, r execx.Runner, path string) error {
	_, err := r.Run(ctx, timeout, "icacls", path, "/inheritance:r", "/grant:r", "*S-1-5-32-544:F", "*S-1-5-18:F")
	return err
}

// Install registers the tasks for the logged-on user (call SecureDir on the
// install folder first). Docker Desktop lives in that user's session, so the
// tasks run only while they are logged on.
func Install(ctx context.Context, r execx.Runner, bin string) error {
	user := currentUser()
	// First run 15 min after install (time to set refuse_if_exists); the
	// daily update 30 min after an hourly run so they don't fight for the lock.
	runAt := now().Add(15 * time.Minute).Format("15:04")
	updateAt := now().Add(45 * time.Minute).Format("15:04")
	desktop := filepath.Join(os.Getenv("ProgramFiles"), "Docker", "Docker", "Docker Desktop.exe")
	cmds := [][]string{
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-run", "/SC", "HOURLY", "/ST", runAt, "/TR", headless(bin, "run")},
		{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
			"/TN", "runner-guard-update", "/SC", "DAILY", "/ST", updateAt, "/TR", headless(bin, "update")},
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

// headless runs the console exe without a window flashing up on the
// logged-on user's desktop.
func headless(bin, cmd string) string { return `conhost.exe --headless "` + bin + `" ` + cmd }

// sampleCmd: every 5 minutes.
func sampleCmd(bin, user string) []string {
	return []string{"schtasks", "/Create", "/F", "/RL", "HIGHEST", "/IT", "/RU", user,
		"/TN", sampleTask, "/SC", "MINUTE", "/MO", "5", "/TR", headless(bin, "sample")}
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
