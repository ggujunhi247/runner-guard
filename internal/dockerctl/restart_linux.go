package dockerctl

import (
	"context"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

func Restart(ctx context.Context, r execx.Runner) error {
	_, err := r.Run(ctx, timeout, "systemctl", "restart", "docker")
	return err
}
