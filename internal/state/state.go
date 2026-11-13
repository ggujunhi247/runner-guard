// Package state keeps what runner-guard remembers between runs.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	DockerRestarts []time.Time `json:"docker_restarts"`
	BlockedByUs    bool        `json:"blocked_by_us"`
	LastAction     string      `json:"last_action"`
	LastRun        time.Time   `json:"last_run"`
	BlockReason    string      `json:"block_reason"`
	LastReclaim    time.Time   `json:"last_reclaim"`
}

// Load never fails: a missing file is a fresh start, an empty or corrupt one
// is a fresh start plus a warning.
func Load(path string) (State, string) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, ""
	}
	if err != nil {
		return State{}, "state file unreadable, starting fresh: " + err.Error()
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return State{}, "state file empty, starting fresh"
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, "state file corrupt, starting fresh: " + err.Error()
	}
	return s, ""
}

// Save writes atomically: temp file in the same directory, then rename.
func Save(path string, s State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
