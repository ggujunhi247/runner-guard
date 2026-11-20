package dockerctl

import (
	"context"
	"strings"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// Restart uses `docker desktop restart` when this Docker Desktop has it.
// Otherwise it kills Docker Desktop and relaunches it through a non-admin
// task, because an elevated Docker Desktop behaves differently.
func Restart(ctx context.Context, r execx.Runner) error {
	if _, err := r.Run(ctx, timeout, "docker", "desktop", "version"); err == nil {
		_, err = r.Run(ctx, timeout, "docker", "desktop", "restart")
		if err == nil || strings.Contains(err.Error(), "timed out") {
			return err // a slow restart may still finish; killing it mid-way does harm
		}
		// The plugin answers even with Desktop not running (say, after a
		// reboot); fall through and launch it.
	}
	r.Run(ctx, timeout, "taskkill", "/F", "/IM", "Docker Desktop.exe") // fails if not running; fine
	_, err := r.Run(ctx, timeout, "schtasks", "/Run", "/TN", DesktopTask)
	return err
}
