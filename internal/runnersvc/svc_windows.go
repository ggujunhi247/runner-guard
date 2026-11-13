package runnersvc

import (
	"context"
	"strings"

	"github.com/ggujunhi/runner-guard/internal/execx"
)

func Status(ctx context.Context, r execx.Runner, name string) (running bool, exists bool, err error) {
	out, err := r.Run(ctx, timeout, "sc.exe", "query", name)
	if err != nil {
		if strings.Contains(out, "1060") { // ERROR_SERVICE_DOES_NOT_EXIST
			return false, false, nil
		}
		return false, false, err
	}
	return strings.Contains(out, "RUNNING"), true, nil
}

func Stop(ctx context.Context, r execx.Runner, name string) error {
	_, err := r.Run(ctx, timeout, "sc.exe", "stop", name)
	return err
}

func Start(ctx context.Context, r execx.Runner, name string) error {
	_, err := r.Run(ctx, timeout, "sc.exe", "start", name)
	return err
}
