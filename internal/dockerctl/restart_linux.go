package dockerctl

import (
	"context"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func Restart(ctx context.Context, r execx.Runner) error {
	_, err := r.Run(ctx, timeout, "systemctl", "restart", "docker")
	return err
}
