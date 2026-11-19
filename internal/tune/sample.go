// Package tune records how the runner host is used and turns that history
// into small, reversible setting changes.
package tune

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/disk"
	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/execx"
	"github.com/ggujunhi247/runner-guard/internal/pressure"
	"github.com/ggujunhi247/runner-guard/internal/runnersvc"
	"github.com/ggujunhi247/runner-guard/internal/state"
)

// Keep is how much history is kept.
const Keep = 30 * 24 * time.Hour

const jobLabel = "com.gitlab.gitlab-runner.job.id"

// Sample is one line of history.jsonl. Memory fields describe the memory
// Docker runs in: the host on Linux, the WSL VM on Windows.
type Sample struct {
	T          time.Time `json:"t"`
	Kind       string    `json:"kind,omitempty"` // "" every 5 min; "run" = hourly disk reading
	Skip       string    `json:"skip,omitempty"` // why this sample must not drive tuning
	Jobs       int       `json:"jobs"`
	Concurrent int       `json:"concurrent,omitempty"`
	MemTotalMB float64   `json:"mem_total_mb,omitempty"`
	MemAvailMB float64   `json:"mem_avail_mb,omitempty"`
	SwapMB     float64   `json:"swap_mb,omitempty"`
	SwapFreeMB float64   `json:"swap_free_mb,omitempty"`
	Pswpin     float64   `json:"pswpin,omitempty"`   // cumulative pages swapped in
	OOMKill    float64   `json:"oom_kill,omitempty"` // cumulative OOM kills
	JobMemMB   []float64 `json:"job_mem_mb,omitempty"`
	OtherMemMB float64   `json:"other_mem_mb,omitempty"` // non-runner containers
	HostMemPct float64   `json:"host_mem_pct,omitempty"`
	HostMemMB  float64   `json:"host_mem_mb,omitempty"`
	Disk       float64   `json:"disk,omitempty"`     // run: first reading
	DiskEnd    float64   `json:"disk_end,omitempty"` // run: after any prune or reclaim
}

func (s Sample) usable() bool { return s.Skip == "" }

// MemPct is used memory of Docker's memory, 0 when unknown.
func (s Sample) MemPct() float64 {
	if s.MemTotalMB <= 0 {
		return 0
	}
	return (s.MemTotalMB - s.MemAvailMB) / s.MemTotalMB * 100
}

// SkipReason says why a sample taken now would mislead tuning: a stopped
// runner or dead engine shows "0 jobs" that is not spare capacity, and the
// hours after a reclaim, restart or update are not normal load.
func SkipReason(st state.State, now time.Time, dockerOK, runnerOK bool) string {
	switch {
	case st.BlockedByUs:
		return "blocked"
	case !dockerOK:
		return "docker"
	case !runnerOK:
		return "runner-stopped"
	case now.Sub(st.LastReclaim) < time.Hour, now.Sub(st.LastDisruptive) < time.Hour:
		return "restart"
	case now.Sub(st.LastUpdate) < 48*time.Hour:
		return "update"
	}
	return ""
}

// Collect takes one sample. Failures leave fields zero; a sample is still written.
func Collect(ctx context.Context, r execx.Runner, cfg config.Config, st state.State, now time.Time) Sample {
	s := Sample{T: now.UTC()}
	dockerOK := dockerctl.Healthy(ctx, r)
	running, _, _ := runnersvc.Status(ctx, r, cfg.RunnerService)
	s.Skip = SkipReason(st, now, dockerOK, running)
	if n, err := ReadConcurrent(cfg.RunnerConfig); err == nil {
		s.Concurrent = n
	}
	if p, err := pressure.Read(); err == nil {
		s.HostMemPct, s.HostMemMB = p.MemPct, p.TotalMB
	}
	if !dockerOK {
		return s
	}
	txt, err := memText(ctx, r)
	kv := parseKV(txt)
	if err != nil || kv["MemTotal"] <= 0 {
		// Zero counters would read as a huge swap-in or OOM jump next time.
		if s.Skip == "" {
			s.Skip = "mem"
		}
		return s
	}
	s.MemTotalMB, s.MemAvailMB = kv["MemTotal"]/1024, kv["MemAvailable"]/1024
	s.SwapMB, s.SwapFreeMB = kv["SwapTotal"]/1024, kv["SwapFree"]/1024
	s.Pswpin, s.OOMKill = kv["pswpin"], kv["oom_kill"]
	s.Jobs, s.JobMemMB, s.OtherMemMB = containers(ctx, r)
	return s
}

// VMMem returns MemTotal and SwapTotal (MB) of the memory Docker runs in.
func VMMem(ctx context.Context, r execx.Runner) (mem, swap float64, err error) {
	txt, err := memText(ctx, r)
	if err != nil {
		return 0, 0, err
	}
	kv := parseKV(txt)
	return kv["MemTotal"] / 1024, kv["SwapTotal"] / 1024, nil
}

// RunningJobs counts GitLab jobs with a running container; -1 when unknown.
func RunningJobs(ctx context.Context, r execx.Runner) int {
	out, err := r.Run(ctx, time.Minute, "docker", "ps", "--filter", "status=running", "--format", `{{.ID}} {{.Label "`+jobLabel+`"}}`)
	if err != nil {
		return -1
	}
	jobs := map[string]bool{}
	for _, j := range parseJobIDs(out) {
		if j != "" {
			jobs[j] = true
		}
	}
	return len(jobs)
}

func containers(ctx context.Context, r execx.Runner) (jobs int, jobMem []float64, other float64) {
	out, err := r.Run(ctx, time.Minute, "docker", "ps", "--filter", "status=running", "--format", `{{.ID}} {{.Label "`+jobLabel+`"}}`)
	if err != nil {
		return 0, nil, 0
	}
	owner := parseJobIDs(out)
	perJob := map[string]float64{}
	for _, j := range owner {
		if j != "" {
			perJob[j] += 0
		}
	}
	if len(owner) > 0 {
		stats, err := r.Run(ctx, time.Minute, "docker", "stats", "--no-stream", "--format", "{{.ID}} {{.MemUsage}}")
		if err == nil {
			for id, mb := range parseStats(stats) {
				if j := owner[id]; j != "" {
					perJob[j] += mb
				} else if _, known := owner[id]; known {
					other += mb
				}
			}
		}
	}
	for _, mb := range perJob {
		if mb > 0 {
			jobMem = append(jobMem, mb)
		}
	}
	sort.Float64s(jobMem)
	return len(perJob), jobMem, other
}

// parseJobIDs maps container ID to its GitLab job id ("" = not a job container).
func parseJobIDs(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch len(f) {
		case 1:
			m[f[0]] = ""
		case 2:
			m[f[0]] = f[1]
		}
	}
	return m
}

// parseStats reads `docker stats --format "{{.ID}} {{.MemUsage}}"` ("12.3MiB / 7.6GiB").
func parseStats(out string) map[string]float64 {
	m := map[string]float64{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if b, err := disk.ParseSize(f[1]); err == nil {
			m[f[0]] = b / (1 << 20)
		}
	}
	return m
}

// parseKV reads /proc/meminfo ("MemTotal: 123 kB") and /proc/vmstat ("pswpin 5").
func parseKV(text string) map[string]float64 {
	m := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if n, err := strconv.ParseFloat(f[1], 64); err == nil {
			m[strings.TrimSuffix(f[0], ":")] = n
		}
	}
	return m
}

// Append adds one sample line.
func Append(path string, s Sample) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Load returns samples newer than since, skipping lines it cannot parse.
func Load(path string, since time.Time) ([]Sample, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Sample
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var s Sample
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.T.After(since) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out, nil
}

// Rewrite replaces the file with h, atomically.
// ponytail: a sample appended between Load and Rewrite is lost; one 5-min
// sample a day does not matter.
func Rewrite(path string, h []Sample) error {
	var buf bytes.Buffer
	for _, s := range h {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		buf.Write(append(b, '\n'))
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
