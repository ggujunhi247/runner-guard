package state

import (
	"errors"
	"os"
	"syscall"
)

// alive: signal 0 checks existence; EPERM still means the process exists.
func alive(pid int) bool {
	p, _ := os.FindProcess(pid)
	err := p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
