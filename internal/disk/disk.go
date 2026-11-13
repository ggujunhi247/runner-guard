// Package disk measures Docker disk use and prunes what the runner left behind.
package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// RunnerLabel matches only containers and volumes GitLab Runner created.
const RunnerLabel = "label=com.gitlab.gitlab-runner.managed=true"

const (
	pruneTimeout = 900 * time.Second
	cmdTimeout   = 60 * time.Second
)

var units = map[string]float64{"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15}

// ParseSize parses Docker's human sizes ("1.5GB", "512kB"); units are decimal.
func ParseSize(s string) (float64, error) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i <= 0 {
		return 0, fmt.Errorf("bad size %q", s)
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, fmt.Errorf("bad size %q", s)
	}
	mult, ok := units[strings.ToLower(strings.TrimSpace(s[i:]))]
	if !ok {
		return 0, fmt.Errorf("bad size unit %q", s)
	}
	return n * mult, nil
}

func ShouldPrune(pct, threshold float64, force bool) bool {
	return force || pct >= threshold
}

// Prune removes stopped runner containers and runner volumes, and, only when
// prune_images is on, unused images and build cache created before image_age.
func Prune(ctx context.Context, r execx.Runner, cfg config.Config) error {
	cmds := [][]string{
		{"container", "prune", "--force", "--filter", RunnerLabel},
		{"volume", "prune", "--all", "--force", "--filter", RunnerLabel},
	}
	if cfg.PruneImages {
		until := "until=" + cfg.ImageAge
		cmds = append(cmds,
			[]string{"image", "prune", "--all", "--force", "--filter", until},
			[]string{"builder", "prune", "--force", "--filter", until})
	}
	var errs []error
	for _, c := range cmds {
		if _, err := r.Run(ctx, pruneTimeout, "docker", c...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// desktopPercent: Docker Desktop's VHDX never shrinks, so take the worse of
// "Docker data vs budget" and "host drive used".
func desktopPercent(heldBytes, budgetBytes, hostUsedPct float64) float64 {
	if budgetBytes <= 0 {
		return hostUsedPct
	}
	return max(heldBytes/budgetBytes*100, hostUsedPct)
}

// sumDF adds up Size from `docker system df --format {{json .}}` lines.
func sumDF(out string) (float64, error) {
	var total float64
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row struct{ Size string }
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return 0, fmt.Errorf("docker system df: %w", err)
		}
		n, err := ParseSize(row.Size)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
