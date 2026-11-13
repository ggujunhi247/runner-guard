package dockerctl

import (
	"context"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

// Restart uses `docker desktop restart` when this Docker Desktop has it.
// Otherwise it kills Docker Desktop and relaunches it through a non-admin
// task, because an elevated Docker Desktop behaves differently.
func Restart(ctx context.Context, r execx.Runner) error {
	if _, err := r.Run(ctx, timeout, "docker", "desktop", "version"); err == nil {
		_, err = r.Run(ctx, timeout, "docker", "desktop", "restart")
		return err
	}
	r.Run(ctx, timeout, "taskkill", "/F", "/IM", "Docker Desktop.exe") // fails if not running; fine
	_, err := r.Run(ctx, timeout, "schtasks", "/Run", "/TN", DesktopTask)
	return err
}
