package config

import (
	"os"
	"path/filepath"
)

func Dir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "runner-guard")
}
func File() string    { return filepath.Join(Dir(), "config.json") }
func BinPath() string { return filepath.Join(Dir(), "runner-guard.exe") }

// The Windows runner is a CI-only Docker Desktop host: prune images and
// build cache, and give freed space back to the drive.
func osDefaults(c *Config) {
	c.PruneImages = true
	c.DesktopReclaim = true
	c.RunnerConfig = `C:\GitLab-Runner\config.toml`
	// Only after Docker stays dead through restarts and a WSL restart, at
	// most weekly, and only with automatic logon (the tasks need a session).
	c.RebootRecover = true
}
