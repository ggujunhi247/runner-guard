package runnersvc

import (
	"context"
	"strings"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// serviceCommand reads BINARY_PATH_NAME from `sc.exe qc`.
func serviceCommand(ctx context.Context, r execx.Runner, name string) string {
	out, err := r.Run(ctx, timeout, "sc.exe", "qc", name)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(out, "\n") {
		if _, v, ok := strings.Cut(l, "BINARY_PATH_NAME"); ok {
			if _, v, ok = strings.Cut(v, ":"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}
