// Package fsx is the one way runner-guard writes a file (internal/policy
// forbids os.WriteFile and os.Create elsewhere).
package fsx

import (
	"os"
	"path/filepath"
	"time"
)

// WriteFile replaces path with data: a fresh temp file next to it (O_EXCL, so
// never one someone else made), then a rename. It never truncates or writes
// through whatever is at path, a symlink included, and readers never see half
// a file.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	// CreateTemp: a random name, created exclusively, so two writers never
	// share a temp file and a planted one is never reused.
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".rg-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = f.Chmod(mode)
	if _, werr := f.Write(data); err == nil {
		err = werr
	}
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	// Windows refuses to replace a file another process has open (a sample
	// appending history); that is over in moments.
	for i := 0; ; i++ {
		if err = os.Rename(tmp, path); err == nil || i == 5 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
