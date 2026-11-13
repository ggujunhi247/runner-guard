package update

import "os"

func oldPath(bin string) string { return bin + ".old" }

// swap keeps a hard link to the old binary for rollback, then renames the
// new file over the binary in one atomic step.
func swap(bin, newPath string) error {
	old := oldPath(bin)
	os.Remove(old)
	if err := os.Link(bin, old); err != nil {
		return err
	}
	return os.Rename(newPath, bin)
}

// Linux can delete the old binary while it runs.
func afterSwap(bin string) { os.Remove(oldPath(bin)) }
