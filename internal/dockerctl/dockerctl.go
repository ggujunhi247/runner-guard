// Package dockerctl checks and restarts the Docker engine.
package dockerctl

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// DesktopTask is the non-admin scheduled task that relaunches Docker Desktop.
const DesktopTask = "runner-guard-desktop"

const timeout = 60 * time.Second

// Check returns nil when the engine answers. A missing docker CLI comes back
// as execx.ErrNotFound so callers can tell "misconfigured" from "dead".
func Check(ctx context.Context, r execx.Runner) error {
	out, err := r.Run(ctx, timeout, "docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return errors.New("docker info: no server version")
	}
	return nil
}

func Healthy(ctx context.Context, r execx.Runner) bool { return Check(ctx, r) == nil }

// CanRestart enforces the minimum interval and the per-24h cap.
func CanRestart(now time.Time, history []time.Time, minInterval time.Duration, maxPerDay int) bool {
	recent := 0
	for _, t := range history {
		age := now.Sub(t)
		if age < minInterval {
			return false
		}
		if age < 24*time.Hour {
			recent++
		}
	}
	return recent < maxPerDay
}

// Trim drops restarts older than 24h.
func Trim(now time.Time, history []time.Time) []time.Time {
	var kept []time.Time
	for _, t := range history {
		if now.Sub(t) < 24*time.Hour {
			kept = append(kept, t)
		}
	}
	return kept
}
