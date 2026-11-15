// Package pressure samples host resource pressure. runner-guard only
// reports and alerts on it; it never tunes or kills anything for it.
package pressure

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Thresholds for "this host is struggling". Hourly samples, so a value must
// stay over them for SustainedRuns runs in a row before anyone is paged.
// ponytail: fixed thresholds; make them config keys if hosts differ a lot.
const (
	MemHighPct     = 90
	LoadHighPerCPU = 2.0
	DockerSlow     = 20 * time.Second
	SustainedRuns  = 3
)

type Sample struct {
	MemPct     float64 // used physical memory, percent
	LoadPerCPU float64 // 1-minute load / CPU count; -1 where the OS has no load average
}

// Reasons lists what is over its threshold; empty means fine.
func Reasons(s Sample, dockerLatency time.Duration) []string {
	var r []string
	if s.MemPct >= MemHighPct {
		r = append(r, fmt.Sprintf("memory %.0f%%", s.MemPct))
	}
	if s.LoadPerCPU >= LoadHighPerCPU {
		r = append(r, fmt.Sprintf("load %.1f per CPU", s.LoadPerCPU))
	}
	if dockerLatency >= DockerSlow {
		r = append(r, fmt.Sprintf("docker info took %s", dockerLatency.Round(time.Second)))
	}
	return r
}

// parseMeminfo returns used percent from /proc/meminfo (MemAvailable based).
func parseMeminfo(text string) (float64, error) {
	var total, avail float64
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = n
		case "MemAvailable:":
			avail = n
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("meminfo: no MemTotal")
	}
	return (total - avail) / total * 100, nil
}

// parseLoadavg returns the 1-minute load divided by ncpu.
func parseLoadavg(text string, ncpu int) (float64, error) {
	f := strings.Fields(text)
	if len(f) == 0 || ncpu <= 0 {
		return 0, fmt.Errorf("loadavg: %q", text)
	}
	l, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, fmt.Errorf("loadavg: %w", err)
	}
	return l / float64(ncpu), nil
}
