package guard

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/state"
	"github.com/ggujunhi247/runner-guard/internal/tune"
)

// restartVM restarts the WSL VM and Docker Desktop and waits up to 10 min.
func restartVM(ctx context.Context, d Deps) error {
	// Stop Desktop first, as Reclaim does: killing the VM under a running
	// Desktop leaves it on an error dialog that a relaunch does not clear.
	if _, err := d.R.Run(ctx, 3*time.Minute, "docker", "desktop", "stop"); err != nil {
		d.R.Run(ctx, time.Minute, "taskkill", "/F", "/IM", "Docker Desktop.exe")
	}
	if _, err := d.R.Run(ctx, 3*time.Minute, "wsl", "--shutdown"); err != nil {
		return fmt.Errorf("wsl --shutdown: %w", err)
	}
	if _, err := d.R.Run(ctx, time.Minute, "schtasks", "/Run", "/TN", dockerctl.DesktopTask); err != nil {
		return fmt.Errorf("relaunch Docker Desktop: %w", err)
	}
	for i := 0; i < 60; i++ {
		d.sleep(10 * time.Second)
		if dockerctl.Healthy(ctx, d.R) {
			return nil
		}
	}
	return fmt.Errorf("docker did not answer within 10 min of the WSL restart")
}

// osRecover is the step between "restart cap reached" and a reboot.
func osRecover(ctx context.Context, d Deps) error { return restartVM(ctx, d) }

// applyWSL writes memory= or swap= into the user's .wslconfig while the runner
// is idle and stopped, restarts the VM, and checks the result. Any failure
// puts the previous file back.
func applyWSL(ctx context.Context, d Deps, st *state.State, key string, gb int, now time.Time) error {
	idle := func() bool { return tune.RunningJobs(ctx, d.R) == 0 }
	if !idle() {
		return errBusy
	}
	d.sleep(time.Minute)
	if !idle() {
		return errBusy
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".wslconfig")
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	edited, err := tune.SetWSL(old, key, gb)
	if err != nil {
		return err
	}

	// If this process dies mid-way, the next run starts the runner again
	// (reason "tune" lifts like a Docker block).
	name := d.Cfg.RunnerService
	running, _, err := runnersvc.Status(ctx, d.R, name)
	if err != nil {
		return fmt.Errorf("%w: runner status unknown: %v", errBusy, err) // can't be sure no job starts
	}
	st.LastDisruptive = now // samples skip the next hour
	if running {
		if err := d.stopRunner(ctx, st, runnersvc.ReasonTune, now); err != nil {
			return fmt.Errorf("%w: %v", errBusy, err)
		}
		defer func() {
			// With Docker dead the runner would only fail jobs; the flag stays
			// and the next run starts it once Docker answers.
			if !dockerctl.Healthy(ctx, d.R) {
				return
			}
			if err := runnersvc.Start(ctx, d.R, name); err == nil {
				st.BlockedByUs, st.BlockReason = false, ""
			}
		}()
	}
	if !idle() { // a job slipped in before the stop
		return errBusy
	}
	if existed {
		os.WriteFile(filepath.Join(filepath.Dir(d.HistoryPath), "wslconfig.last-good"), old, 0o644)
	}
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		return err
	}
	verr := restartVM(ctx, d)
	if verr == nil {
		mem, swap, err := tune.VMMem(ctx, d.R)
		got := mem / 1024
		if key == "swap" {
			got = swap / 1024
		}
		if err != nil || math.Abs(got-float64(gb)) > float64(gb)*0.1 {
			verr = fmt.Errorf("VM %s is %.1fGB after setting %dGB (%v)", key, got, gb, err)
		}
	}
	if verr == nil {
		return nil
	}
	if existed {
		os.WriteFile(path, old, 0o644)
	} else {
		os.Remove(path)
	}
	if err := restartVM(ctx, d); err != nil {
		verr = fmt.Errorf("%v; restoring the old .wslconfig also failed: %v", verr, err)
	}
	return verr
}

// canReboot: the scheduled tasks need a logged-on user, so a reboot only
// helps when Windows logs on by itself.
func CanReboot(ctx context.Context, d Deps) error {
	out, err := d.R.Run(ctx, time.Minute, "reg", "query", `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`, "/v", "AutoAdminLogon")
	f := strings.Fields(out)
	if err != nil || len(f) == 0 || f[len(f)-1] != "1" {
		return fmt.Errorf("automatic logon is off; after a reboot nothing would start Docker Desktop")
	}
	return nil
}

func reboot(ctx context.Context, d Deps) error {
	_, err := d.R.Run(ctx, time.Minute, "shutdown", "/r", "/t", "120", "/c", "runner-guard: Docker did not come back after restarts")
	return err
}
