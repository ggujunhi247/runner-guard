package runnersvc

import (
	"context"
	"strings"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// serviceCommand: systemd prints ExecStart={ path=... ; argv[]=/usr/bin/gitlab-runner run --config ... ; ... }
func serviceCommand(ctx context.Context, r execx.Runner, name string) string {
	out, err := r.Run(ctx, timeout, "systemctl", "show", "-p", "ExecStart", "--value", name)
	if err != nil {
		return ""
	}
	if _, rest, ok := strings.Cut(out, "argv[]="); ok {
		out = rest
	}
	return strings.TrimSpace(out)
}
