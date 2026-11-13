package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrLocked = errors.New("another runner-guard is running")

// Lock creates path exclusively. A lock older than stale is taken over.
func Lock(path string, stale time.Duration) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue // removed meanwhile; try again
		}
		if time.Since(fi.ModTime()) < stale {
			return nil, ErrLocked
		}
		os.Remove(path)
	}
	return nil, ErrLocked
}
