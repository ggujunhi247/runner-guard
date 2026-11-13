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

var timers = []string{"runner-guard-run.timer", "runner-guard-update.timer"}

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

const runTimer = `[Unit]
Description=runner-guard run every hour

[Timer]
OnBootSec=5min
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

func units(bin string) map[string]string {
	return map[string]string{
		"runner-guard-run.service":    service(bin, "run"),
		"runner-guard-run.timer":      runTimer,
		"runner-guard-update.service": service(bin, "update"),
		"runner-guard-update.timer":   updateTimer,
	}
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
