// Package config loads the host's runner-guard settings.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	PrunePercent             float64  `json:"prune_percent"`
	BlockPercent             float64  `json:"block_percent"`
	DesktopBudgetGB          float64  `json:"desktop_budget_gb"`
	ImageAge                 string   `json:"image_age"`
	PruneImages              bool     `json:"prune_images"`
	DesktopReclaim           bool     `json:"desktop_reclaim"`
	DesktopVHDX              string   `json:"desktop_vhdx"`
	DockerRestartMinInterval string   `json:"docker_restart_min_interval"`
	DockerRestartMaxPerDay   int      `json:"docker_restart_max_per_day"`
	RunnerService            string   `json:"runner_service"`
	RefuseIfExists           []string `json:"refuse_if_exists"`
	StuckContainerAge        string   `json:"stuck_container_age"`
	AlertWebhook             string   `json:"alert_webhook"`
	UpdateRepo               string   `json:"update_repo"`
	AutoUpdate               bool     `json:"auto_update"`
	// AutoTune lets the daily tune pass change concurrent, WSL memory/swap and
	// lower prune_percent once enough history exists. False: report only.
	AutoTune      bool   `json:"auto_tune"`
	RunnerConfig  string `json:"runner_config"`  // gitlab-runner config.toml
	RebootRecover bool   `json:"reboot_recover"` // reboot when Docker stays dead after restarts
}

func Default() Config {
	c := Config{
		PrunePercent:             75,
		BlockPercent:             90,
		DesktopBudgetGB:          60,
		ImageAge:                 "72h",
		DockerRestartMinInterval: "30m",
		DockerRestartMaxPerDay:   3,
		RunnerService:            "gitlab-runner",
		RefuseIfExists:           []string{},
		// Well above GitLab's default 1h job timeout; raise it if jobs may run longer.
		StuckContainerAge: "6h",
		UpdateRepo:        "ggujunhi247/runner-guard",
		AutoUpdate:        true,
		AutoTune:          true,
	}
	osDefaults(&c)
	return c
}

// Load reads path over the defaults. A missing file gives Default().
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return c, fmt.Errorf("%s is UTF-16; save it as UTF-8", path)
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}) // Notepad's BOM
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Durations validates and parses the duration settings.
func (c Config) Durations() (imageAge string, restartMin time.Duration, err error) {
	if _, err := time.ParseDuration(c.ImageAge); err != nil {
		return "", 0, fmt.Errorf("image_age: %w", err)
	}
	restartMin, err = time.ParseDuration(c.DockerRestartMinInterval)
	if err != nil {
		return "", 0, fmt.Errorf("docker_restart_min_interval: %w", err)
	}
	if _, err := c.StuckAge(); err != nil {
		return "", 0, err
	}
	return c.ImageAge, restartMin, nil
}

// Clamp fixes thresholds that cannot work: block_percent is the safety stop
// (at most 95) and prune must start below it. It never fails, so an old config
// cannot stop the guard from starting a runner it blocked; it returns a warning.
func (c *Config) Clamp() string {
	was := fmt.Sprintf("prune_percent %g, block_percent %g", c.PrunePercent, c.BlockPercent)
	changed := false
	if c.BlockPercent > 95 || c.BlockPercent <= 10 {
		c.BlockPercent, changed = min(max(c.BlockPercent, 20), 95), true
	}
	if c.PrunePercent <= 0 || c.PrunePercent >= c.BlockPercent {
		c.PrunePercent, changed = c.BlockPercent-10, true
	}
	if !changed {
		return ""
	}
	return fmt.Sprintf("config %s is unusable; using prune_percent %g, block_percent %g (need 0 < prune < block <= 95)", was, c.PrunePercent, c.BlockPercent)
}

// StuckAge parses stuck_container_age; 0 means off. Values under an hour
// are rejected: set it well above the longest GitLab job timeout.
func (c Config) StuckAge() (time.Duration, error) {
	if c.StuckContainerAge == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.StuckContainerAge)
	if err != nil {
		return 0, fmt.Errorf("stuck_container_age: %w", err)
	}
	if d < time.Hour {
		return 0, fmt.Errorf("stuck_container_age %s is under 1h; it must exceed the longest job timeout", d)
	}
	return d, nil
}
