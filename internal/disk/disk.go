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

var units = map[string]float64{"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40}

// ParseSize parses Docker's human sizes: "1.5GB", "512kB" (decimal) and
// "12.3MiB" (binary, as docker stats prints).
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

// TestcontainersLabel matches what Testcontainers leaves behind on the host Docker.
const TestcontainersLabel = "label=org.testcontainers=true"

// Step is one docker prune command and the space it reported freeing.
type Step struct {
	Args      []string
	Reclaimed float64 // bytes; 0 when docker printed no size
}

// Total adds up the bytes freed by steps.
func Total(steps []Step) float64 {
	var n float64
	for _, s := range steps {
		n += s.Reclaimed
	}
	return n
}

// reclaimed reads the "Total reclaimed space: 1.5GB" line of a prune's output.
func reclaimed(out string) float64 {
	const key = "Total reclaimed space:"
	for _, line := range strings.Split(out, "\n") {
		if _, v, ok := strings.Cut(line, key); ok {
			if n, err := ParseSize(v); err == nil {
				return n
			}
		}
	}
	return 0
}

func runSteps(ctx context.Context, r execx.Runner, cmds [][]string) ([]Step, error) {
	var steps []Step
	var errs []error
	for _, c := range cmds {
		out, err := r.Run(ctx, pruneTimeout, "docker", c...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		steps = append(steps, Step{Args: c, Reclaimed: reclaimed(out)})
	}
	return steps, errors.Join(errs...)
}

// Prune removes stopped runner containers, runner volumes and unused runner
// networks; when prune_testcontainers is on, the same for Testcontainers
// leftovers (containers only if stopped and older than 1h); and, only when
// prune_images is on, unused images and build cache created before image_age.
func Prune(ctx context.Context, r execx.Runner, cfg config.Config) ([]Step, error) {
	cmds := [][]string{
		{"container", "prune", "--force", "--filter", RunnerLabel},
		{"volume", "prune", "--all", "--force", "--filter", RunnerLabel},
		{"network", "prune", "--force", "--filter", RunnerLabel},
	}
	if cfg.PruneTestcontainers {
		cmds = append(cmds,
			[]string{"container", "prune", "--force", "--filter", TestcontainersLabel, "--filter", "until=1h"},
			[]string{"volume", "prune", "--all", "--force", "--filter", TestcontainersLabel},
			[]string{"network", "prune", "--force", "--filter", TestcontainersLabel, "--filter", "until=1h"})
	}
	if cfg.PruneImages {
		until := "until=" + cfg.ImageAge
		cmds = append(cmds,
			[]string{"image", "prune", "--all", "--force", "--filter", until},
			[]string{"builder", "prune", "--force", "--filter", until})
	}
	return runSteps(ctx, r, cmds)
}

// PruneDeep runs when usage is still over prune_percent after Prune: build
// cache and unused images of any age. Only with prune_images; otherwise it
// does nothing (runner networks were already pruned by Prune, which has no
// age filter).
func PruneDeep(ctx context.Context, r execx.Runner, cfg config.Config) ([]Step, error) {
	// Its own switch: prune_images alone means "aged images only" and must not
	// start removing today's unused images on a host that also runs production.
	if !cfg.PruneImages || !cfg.PruneDeep {
		return nil, nil
	}
	return runSteps(ctx, r, [][]string{
		{"builder", "prune", "--all", "--force"},
		{"image", "prune", "--all", "--force"},
	})
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
