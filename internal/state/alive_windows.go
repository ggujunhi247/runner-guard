package state

import (
	"errors"
	"os"
	"syscall"
)

// alive: OpenProcess fails with ERROR_INVALID_PARAMETER once the process is
// gone. Access denied (an elevated holder) still means alive.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return !errors.Is(err, syscall.Errno(87))
	}
	p.Release()
	return true
}
