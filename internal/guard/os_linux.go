package guard

import (
	"context"
	"errors"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/state"
)

func osRecover(context.Context, Deps) error { return errors.ErrUnsupported }

func applyWSL(context.Context, Deps, *state.State, string, int, time.Time) error {
	return errors.ErrUnsupported
}

func CanReboot(context.Context, Deps) error { return nil }

func reboot(ctx context.Context, d Deps) error {
	_, err := d.R.Run(ctx, time.Minute, "shutdown", "-r", "+2", "runner-guard: Docker did not come back after restarts")
	return err
}
