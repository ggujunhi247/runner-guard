package config

func Dir() string     { return "/var/lib/runner-guard" }
func File() string    { return "/etc/runner-guard/config.json" }
func BinPath() string { return "/usr/local/bin/runner-guard" }

// Linux runner hosts may also run production containers: keep images and Testcontainers
// leftovers (prune_testcontainers stays false), and
// never reboot on our own (reboot_recover stays false).
func osDefaults(c *Config) {
	c.RunnerConfig = "/etc/gitlab-runner/config.toml"
}
