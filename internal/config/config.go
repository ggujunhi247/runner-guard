// Package config loads the host's runner-guard settings.
package config

import (
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
	AlertWebhook             string   `json:"alert_webhook"`
	UpdateRepo               string   `json:"update_repo"`
	AutoUpdate               bool     `json:"auto_update"`
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
		UpdateRepo:               "ggujunhi247/runner-guard",
		AutoUpdate:               true,
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
	return c.ImageAge, restartMin, nil
}
