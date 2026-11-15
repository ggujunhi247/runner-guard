package disk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// MinStuckAge is the floor for stuck_container_age: below it a typo could
// kill live jobs.
const MinStuckAge = time.Hour

// RemoveStuck force-removes running runner containers started more than age
// ago. A job past its GitLab timeout is dead to GitLab already; its
// container only holds CPU, memory and disk. Returns the removed IDs.
func RemoveStuck(ctx context.Context, r execx.Runner, now time.Time, age time.Duration) ([]string, error) {
	out, err := r.Run(ctx, cmdTimeout, "docker", "ps", "-q", "--no-trunc", "--filter", RunnerLabel, "--filter", "status=running")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return nil, nil
	}
	out, err = r.Run(ctx, cmdTimeout, "docker", append([]string{"inspect", "--format", "{{.Id}} {{.State.StartedAt}}"}, ids...)...)
	if err != nil {
		return nil, err
	}
	stale, err := staleIDs(out, now, age)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, id := range stale {
		if _, err := r.Run(ctx, cmdTimeout, "docker", "rm", "--force", id); err != nil {
			return removed, err
		}
		removed = append(removed, id)
	}
	return removed, nil
}

// staleIDs parses "<id> <StartedAt>" lines and keeps those older than age.
func staleIDs(out string, now time.Time, age time.Duration) ([]string, error) {
	var stale []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("docker inspect: unexpected line %q", line)
		}
		started, err := time.Parse(time.RFC3339Nano, f[1])
		if err != nil {
			return nil, fmt.Errorf("docker inspect: %w", err)
		}
		if now.Sub(started) > age {
			stale = append(stale, f[0])
		}
	}
	return stale, nil
}
