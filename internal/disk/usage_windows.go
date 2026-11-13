package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// Usage is the larger of Docker data vs desktop_budget_gb and the used
// percent of the drive holding Docker Desktop's data disk.
func Usage(ctx context.Context, r execx.Runner, cfg config.Config) (float64, string, error) {
	held, err := dockerHeld(ctx, r)
	if err != nil {
		return 0, "", err
	}
	drive := dataDrive(cfg)
	host, err := hostUsedPct(drive + `\`)
	if err != nil {
		return 0, "", err
	}
	pct := desktopPercent(held, cfg.DesktopBudgetGB*1e9, host)
	return pct, fmt.Sprintf("docker %.1fGB of %.0fGB budget, %s %.1f%% used", held/1e9, cfg.DesktopBudgetGB, drive, host), nil
}

func dockerHeld(ctx context.Context, r execx.Runner) (float64, error) {
	// df walks every volume; it took ~4 min on a real Desktop host, so it
	// gets the prune timeout rather than 60s.
	out, err := r.Run(ctx, pruneTimeout, "docker", "system", "df", "--format", "{{json .}}")
	if err != nil {
		return 0, err
	}
	return sumDF(out)
}

// VHDXPath is Docker Desktop's data disk: desktop_vhdx if set, else
// <CustomWslDistroDir or %LOCALAPPDATA%\Docker\wsl>\disk\docker_data.vhdx.
func VHDXPath(cfg config.Config) string {
	if cfg.DesktopVHDX != "" {
		return cfg.DesktopVHDX
	}
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "Docker", "wsl")
	if b, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "Docker", "settings-store.json")); err == nil {
		var s struct{ CustomWslDistroDir string }
		if json.Unmarshal(b, &s) == nil && s.CustomWslDistroDir != "" {
			base = s.CustomWslDistroDir
		}
	}
	return filepath.Join(base, "disk", "docker_data.vhdx")
}

// dataDrive is the drive holding the data disk, or %SystemDrive% if unknown.
func dataDrive(cfg config.Config) string {
	if _, err := os.Stat(VHDXPath(cfg)); err == nil {
		if v := filepath.VolumeName(VHDXPath(cfg)); v != "" {
			return v
		}
	}
	if d := os.Getenv("SystemDrive"); d != "" {
		return d
	}
	return "C:"
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
