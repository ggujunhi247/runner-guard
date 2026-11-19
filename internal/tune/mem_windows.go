package tune

import (
	"context"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// Docker Desktop's containers live in the WSL VM; host memory says little.
func memText(ctx context.Context, r execx.Runner) (string, error) {
	return r.Run(ctx, time.Minute, "wsl", "-d", "docker-desktop", "-u", "root", "cat", "/proc/meminfo", "/proc/vmstat")
}
