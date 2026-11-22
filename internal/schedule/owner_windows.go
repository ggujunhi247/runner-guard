package schedule

import (
	"fmt"
	"os/user"
	"syscall"
	"unsafe"
)

// advapi32 is a KnownDLL: Windows always loads it from System32.
var getNamedSecurityInfo = syscall.NewLazyDLL("advapi32.dll").NewProc("GetNamedSecurityInfoW")

func ownerSID(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	const seFileObject, ownerInfo = 1, 1
	var owner *syscall.SID
	var sd uintptr
	r, _, _ := getNamedSecurityInfo.Call(uintptr(unsafe.Pointer(p)), seFileObject, ownerInfo,
		uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if r != 0 {
		return "", syscall.Errno(r)
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	return owner.String()
}

// TrustedOwner refuses a folder that already existed and is owned by someone
// other than Administrators, SYSTEM or the admin running install: a normal
// user who made it first may have planted config.json or other files, and
// taking ownership later would not remove them.
func TrustedOwner(dir string) error {
	sid, err := ownerSID(dir)
	if err != nil {
		return fmt.Errorf("owner of %s: %w", dir, err)
	}
	me := ""
	if u, err := user.Current(); err == nil {
		me = u.Uid // the SID on Windows
	}
	if trustedSID(sid, me) {
		return nil
	}
	return fmt.Errorf("%s already exists and is owned by %s, not Administrators: another account may have planted files there; check it, delete it as an administrator and install again", dir, sid)
}

func trustedSID(sid, me string) bool {
	return sid == "S-1-5-32-544" || sid == "S-1-5-18" || (me != "" && sid == me)
}
