package state

import "syscall"

// alive: a process that cannot be opened (ERROR_INVALID_PARAMETER) is gone;
// access denied (an elevated holder) counts as alive. One that opens may
// still have exited while someone holds a handle, so check its exit code.
func alive(pid int) bool {
	const queryLimited, stillActive = 0x1000, 259
	h, err := syscall.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return err != syscall.Errno(87)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if syscall.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == stillActive
}
