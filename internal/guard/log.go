package guard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const maxLog = 1 << 20

// OpenLog opens <dir>/runner-guard.log for append, first moving it to
// runner-guard.log.1 if it has reached 1 MB.
func OpenLog(dir string) (io.WriteCloser, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "runner-guard.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() >= maxLog {
		os.Rename(p, p+".1")
	}
	return os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

// Logf writes one "[runner-guard] <time> <msg>" line.
func Logf(w io.Writer, now time.Time, format string, a ...any) {
	fmt.Fprintf(w, "[runner-guard] %s "+format+"\n", append([]any{now.Format(time.RFC3339)}, a...)...)
}
