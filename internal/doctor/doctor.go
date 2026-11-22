// Package doctor checks, read-only, whether the risky paths (Docker restart,
// WSL resize, recovery reboot, scheduled tasks, self-update) would work on
// this host, without doing any of them. Run it once on every new host.
package doctor

import (
	"context"
	"fmt"
	"io"

	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/guard"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/tune"
)

// Result levels.
const (
	Pass = "PASS"
	Warn = "WARN"
	Fail = "FAIL"
)

type Check struct {
	Level, Name, Detail string
}

// Run returns every check. It only reads: commands it runs are queries, and
// it writes no file (internal/doctor tests enforce that with a recording fake).
func Run(ctx context.Context, d guard.Deps) []Check {
	var out []Check
	add := func(ok bool, failLevel, name, detail string) {
		lvl := Pass
		if !ok {
			lvl = failLevel
		}
		out = append(out, Check{lvl, name, detail})
	}

	err := dockerctl.Check(ctx, d.R)
	add(err == nil, Fail, "docker answers", errText(err))

	running, exists, err := runnersvc.Status(ctx, d.R, d.Cfg.RunnerService)
	add(err == nil && exists, Fail, "runner service "+d.Cfg.RunnerService,
		fmt.Sprintf("exists=%v running=%v %s", exists, running, errText(err)))

	jobs := tune.RunningJobs(ctx, d.R)
	add(jobs >= 0, Fail, "running jobs readable (WSL changes wait for 0)", fmt.Sprintf("%d job(s)", jobs))

	n, err := tune.ReadConcurrent(d.Cfg.RunnerConfig)
	add(err == nil, Warn, "concurrent readable for tuning", fmt.Sprintf("%s: %d %s", d.Cfg.RunnerConfig, n, errText(err)))

	return append(out, osChecks(ctx, d)...)
}

// Print writes the checks and reports whether any failed.
func Print(w io.Writer, cs []Check) (failed bool) {
	for _, c := range cs {
		fmt.Fprintf(w, "%-4s  %s", c.Level, c.Name)
		if c.Detail != "" {
			fmt.Fprintf(w, ": %s", c.Detail)
		}
		fmt.Fprintln(w)
		failed = failed || c.Level == Fail
	}
	return failed
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return "(" + err.Error() + ")"
}
