package runnersvc

import (
	"context"
	"errors"
	"strings"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

func Status(ctx context.Context, r execx.Runner, name string) (running bool, exists bool, err error) {
	if _, err := r.Run(ctx, timeout, "systemctl", "cat", name); err != nil {
		if errors.Is(err, execx.ErrNotFound) {
			return false, false, err
		}
		return false, false, nil
	}
	out, _ := r.Run(ctx, timeout, "systemctl", "is-active", name) // non-zero exit when inactive
	return strings.TrimSpace(out) == "active", true, nil
}

func Stop(ctx context.Context, r execx.Runner, name string) error {
	_, err := r.Run(ctx, timeout, "systemctl", "stop", name)
	return err
}

func Start(ctx context.Context, r execx.Runner, name string) error {
	_, err := r.Run(ctx, timeout, "systemctl", "start", name)
	return err
}
