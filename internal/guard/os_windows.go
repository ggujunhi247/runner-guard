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
	"github.com/ggujunhi247/runner-guard/internal/fsx"
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
		if err := fsx.WriteFile(lastGood(d), old, 0o644); err != nil {
			return fmt.Errorf("back up .wslconfig: %w", err)
		}
	}
	// Recorded before the write: a run that finds it set restores the old file.
	st.PendingWSLKey, st.PendingWSLPath, st.PendingWSLExisted = key, path, existed
	if err := state.Save(d.StatePath, *st); err != nil {
		st.PendingWSLKey, st.PendingWSLPath = "", ""
		return fmt.Errorf("save state before changing .wslconfig: %w", err)
	}
	done := func() error {
		st.PendingWSLKey, st.PendingWSLPath, st.PendingWSLExisted = "", "", false
		return state.Save(d.StatePath, *st)
	}
	if err := fsx.WriteFile(path, edited, 0o644); err != nil {
		done()
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
		// Saved now, not at the end of Run: a later crash must not undo a verified change.
		return done()
	}
	var rerr error
	if existed {
		rerr = fsx.WriteFile(path, old, 0o644)
	} else if rerr = os.Remove(path); os.IsNotExist(rerr) {
		rerr = nil
	}
	if rerr != nil { // Pending stays set: the next run tries the restore again
		return fmt.Errorf("%v; putting the old .wslconfig back failed: %v", verr, rerr)
	}
	done()
	if err := restartVM(ctx, d); err != nil {
		verr = fmt.Errorf("%v; restoring the old .wslconfig also failed: %v", verr, err)
	}
	return verr
}

func lastGood(d Deps) string {
	return filepath.Join(filepath.Dir(d.HistoryPath), "wslconfig.last-good")
}

// resumeWSL finishes a .wslconfig change that a crash interrupted: the
// unverified file goes, the old one comes back, the VM restarts, and that knob
// is frozen so the same change is not tried again without a person.
func resumeWSL(ctx context.Context, d Deps, st *state.State) []string {
	if st.PendingWSLPath == "" {
		return nil
	}
	path, key := st.PendingWSLPath, st.PendingWSLKey
	var err error
	if st.PendingWSLExisted {
		var old []byte
		if old, err = os.ReadFile(lastGood(d)); err == nil {
			err = fsx.WriteFile(path, old, 0o644)
		}
	} else if err = os.Remove(path); os.IsNotExist(err) {
		err = nil
	}
	k := &st.WSLMemoryGB
	if key == "swap" {
		k = &st.WSLSwapGB
	}
	alreadyTold := k.Frozen
	k.Frozen = true
	if err != nil { // stays pending: tried again next run; alert once, status keeps showing it
		if alreadyTold {
			return nil
		}
		return []string{fmt.Sprintf("a .wslconfig %s change was interrupted and the old file could not be put back yet (%v); will retry; check %s; wsl_%s tuning frozen", key, err, path, key)}
	}
	st.PendingWSLKey, st.PendingWSLPath, st.PendingWSLExisted = "", "", false
	msg := fmt.Sprintf("a .wslconfig %s change was interrupted; put the old file back", key)
	frozen := fmt.Sprintf("; wsl_%s tuning frozen until runner-guard tune --reset", key)
	// The restart kills every job in the VM: only when idle, with the runner
	// stopped the recorded way (the next run starts it once Docker answers).
	// Otherwise the old file simply takes effect at the next WSL restart.
	if tune.RunningJobs(ctx, d.R) != 0 {
		return []string{msg + " (takes effect at the next WSL restart; jobs are running)" + frozen}
	}
	if running, _, err := runnersvc.Status(ctx, d.R, d.Cfg.RunnerService); err != nil || running {
		if err != nil || d.stopRunner(ctx, st, runnersvc.ReasonTune, d.Now()) != nil || tune.RunningJobs(ctx, d.R) != 0 {
			return []string{msg + " (takes effect at the next WSL restart)" + frozen}
		}
	}
	st.LastDisruptive = d.Now()
	if err := restartVM(ctx, d); err != nil {
		msg += fmt.Sprintf(", but restarting WSL failed: %v", err)
	}
	return []string{msg + frozen}
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
