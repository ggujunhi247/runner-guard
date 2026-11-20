// Package schedule registers runner-guard with the OS scheduler.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

var unitDir = "/etc/systemd/system"

const timeout = 60 * time.Second

var timers = []string{"runner-guard-run.timer", "runner-guard-update.timer", "runner-guard-sample.timer"}

func service(bin, cmd string) string {
	return fmt.Sprintf(`[Unit]
Description=runner-guard %[2]s
After=docker.service

[Service]
Type=oneshot
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=%[1]s %[2]s
`, bin, cmd)
}

// OnActiveSec, not OnBootSec: on a host that has been up a while an elapsed
// OnBootSec fires at once on install, before refuse_if_exists can be set.
// The timer is also activated at boot, so this covers boots too.
const runTimer = `[Unit]
Description=runner-guard run every hour

[Timer]
OnActiveSec=15min
OnUnitActiveSec=1h
Persistent=true

[Install]
WantedBy=timers.target
`

const updateTimer = `[Unit]
Description=runner-guard update daily

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
`

const sampleTimer = `[Unit]
Description=runner-guard usage sample every 5 minutes

[Timer]
OnActiveSec=5min
OnUnitActiveSec=5min
AccuracySec=30s

[Install]
WantedBy=timers.target
`

func units(bin string) map[string]string {
	return map[string]string{
		"runner-guard-run.service":    service(bin, "run"),
		"runner-guard-run.timer":      runTimer,
		"runner-guard-update.service": service(bin, "update"),
		"runner-guard-update.timer":   updateTimer,
		"runner-guard-sample.service": service(bin, "sample"),
		"runner-guard-sample.timer":   sampleTimer,
	}
}

// EnsureSample adds the sample timer on hosts installed before it existed.
func EnsureSample(ctx context.Context, r execx.Runner, bin string) (bool, error) {
	if _, err := os.Stat(filepath.Join(unitDir, "runner-guard-sample.timer")); err == nil {
		return false, nil
	}
	u := units(bin)
	for _, name := range []string{"runner-guard-sample.service", "runner-guard-sample.timer"} {
		if err := os.WriteFile(filepath.Join(unitDir, name), []byte(u[name]), 0o644); err != nil {
			return false, err
		}
	}
	if _, err := r.Run(ctx, timeout, "systemctl", "daemon-reload"); err != nil {
		return false, err
	}
	_, err := r.Run(ctx, timeout, "systemctl", "enable", "--now", "runner-guard-sample.timer")
	return err == nil, err
}

func Install(ctx context.Context, r execx.Runner, bin string) error {
	for name, body := range units(bin) {
		if err := os.WriteFile(filepath.Join(unitDir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if _, err := r.Run(ctx, timeout, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err := r.Run(ctx, timeout, "systemctl", append([]string{"enable", "--now"}, timers...)...)
	return err
}

func Uninstall(ctx context.Context, r execx.Runner) error {
	var errs []error
	if _, err := r.Run(ctx, timeout, "systemctl", append([]string{"disable", "--now"}, timers...)...); err != nil {
		errs = append(errs, err)
	}
	for name := range units("") {
		if err := os.Remove(filepath.Join(unitDir, name)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if _, err := r.Run(ctx, timeout, "systemctl", "daemon-reload"); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// SecureDir: the Linux install paths live in root-only parents
// (/usr/local/bin, /etc, /var/lib), so nobody else can have made them first.
func SecureDir(context.Context, execx.Runner, string) error { return nil }

// LockFile makes path root-only (the config holds the webhook URL).
func LockFile(_ context.Context, _ execx.Runner, path string) error { return os.Chmod(path, 0o600) }
