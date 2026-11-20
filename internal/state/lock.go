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
// unlock removes the file only while it still holds our token, so a run
// that outlived stale never deletes its successor's lock.
func Lock(path string, stale time.Duration) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano())
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.WriteString(token)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(path)
				return nil, werr
			}
			return func() {
				if b, err := os.ReadFile(path); err == nil && string(b) == token {
					os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue // removed meanwhile; try again
		}
		// A crashed or rebooted run leaves its lock behind; don't wait out stale
		// when its process is gone. A reused PID just falls back to the age.
		var pid int
		if b, err := os.ReadFile(path); err == nil {
			fmt.Sscan(string(b), &pid)
		}
		if time.Since(fi.ModTime()) < stale && (pid <= 0 || alive(pid)) {
			return nil, ErrLocked
		}
		// Rename, not remove: of several contenders only one moves the stale file.
		// ponytail: a contender that stat'ed the stale file can still rename a
		// fresh lock in the microseconds after takeover; needs two runs starting
		// together right after a crash. A rename-based lock would close it.
		grave := fmt.Sprintf("%s.stale.%d", path, os.Getpid())
		if os.Rename(path, grave) == nil {
			os.Remove(grave)
		}
	}
	return nil, ErrLocked
}
