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
