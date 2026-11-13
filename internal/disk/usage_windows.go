package disk

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/ggujunhi/runner-guard/internal/config"
	"github.com/ggujunhi/runner-guard/internal/execx"
)

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// Usage is the larger of Docker data vs desktop_budget_gb and the host drive's used percent.
func Usage(ctx context.Context, r execx.Runner, cfg config.Config) (float64, string, error) {
	// df walks every volume; it took ~4 min on a real Desktop host, so it
	// gets the prune timeout rather than 60s.
	out, err := r.Run(ctx, pruneTimeout, "docker", "system", "df", "--format", "{{json .}}")
	if err != nil {
		return 0, "", err
	}
	held, err := sumDF(out)
	if err != nil {
		return 0, "", err
	}
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	host, err := hostUsedPct(drive + `\`)
	if err != nil {
		return 0, "", err
	}
	pct := desktopPercent(held, cfg.DesktopBudgetGB*1e9, host)
	return pct, fmt.Sprintf("docker %.1fGB of %.0fGB budget, %s %.1f%% used", held/1e9, cfg.DesktopBudgetGB, drive, host), nil
}

func hostUsedPct(path string) (float64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeAvail, total, totalFree uint64
	ok, _, callErr := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeAvail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if ok == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceExW %s: %w", path, callErr)
	}
	if total == 0 {
		return 0, fmt.Errorf("%s: zero size", path)
	}
	return float64(total-totalFree) / float64(total) * 100, nil
}
