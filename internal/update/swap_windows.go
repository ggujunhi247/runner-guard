package update

import (
	"os"
	"strings"
)

func oldPath(bin string) string { return strings.TrimSuffix(bin, ".exe") + ".old.exe" }

// A running exe can be renamed but not overwritten: move it aside first.
func swap(bin, newPath string) error {
	old := oldPath(bin)
	os.Remove(old)
	if err := os.Rename(bin, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, bin); err != nil {
		os.Rename(old, bin)
		return err
	}
	return nil
}

// The old exe may still be running; the next `update` removes it via Cleanup.
func afterSwap(string) {}
