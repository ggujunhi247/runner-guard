package disk

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/ggujunhi/runner-guard/internal/config"
	"github.com/ggujunhi/runner-guard/internal/execx"
)

// Usage is the used percent of the filesystem holding DockerRootDir.
func Usage(ctx context.Context, r execx.Runner, cfg config.Config) (float64, string, error) {
	out, err := r.Run(ctx, cmdTimeout, "docker", "info", "--format", "{{.DockerRootDir}}")
	if err != nil {
		return 0, "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return 0, "", fmt.Errorf("docker info returned no DockerRootDir")
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		return 0, "", fmt.Errorf("statfs %s: %w", root, err)
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	free := float64(st.Bfree) * float64(st.Bsize)
	if total == 0 {
		return 0, "", fmt.Errorf("statfs %s: zero size", root)
	}
	pct := (total - free) / total * 100
	return pct, fmt.Sprintf("%s %.1f%% of %.0fGB", root, pct, total/1e9), nil
}
