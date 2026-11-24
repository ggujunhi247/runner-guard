package schedule

import (
	"fmt"
	"os"
	"syscall"
)

// TrustedOwner refuses a folder that already existed and is not owned by
// root, or that group/others may write: anyone else could have planted files.
func TrustedOwner(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("owner of %s: unknown", dir)
	}
	if st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s already exists owned by uid %d with mode %v: only root may own and write it. Check its files, then chown -R root: and chmod go-w it, and install again", dir, st.Uid, fi.Mode().Perm())
	}
	return nil
}
