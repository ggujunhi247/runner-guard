package disk

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// Compacting is skipped unless it can win back at least this much.
const reclaimMinGain = 10e9

const compactTimeout = 90 * time.Minute

// Reclaim gives space freed inside Docker Desktop's data disk back to the
// drive. Docker is stopped while the disk is compacted, so call it only
// while the runner is stopped. relaunch must start Docker Desktop again
// (as the logged-on user, not elevated). An empty result means nothing done.
func Reclaim(ctx context.Context, r execx.Runner, cfg config.Config, relaunch func() error) (string, error) {
	vhdx := VHDXPath(cfg)
	fi, err := os.Stat(vhdx)
	if err != nil {
		return "", fmt.Errorf("docker data disk not found (set desktop_vhdx): %w", err)
	}
	// Tell the disk which blocks are free; a sparse VHDX returns them now.
	if _, err := r.Run(ctx, pruneTimeout, "wsl", "-d", "docker-desktop", "-u", "root", "fstrim", "-a"); err != nil {
		return "", fmt.Errorf("fstrim: %w", err)
	}
	if isSparse(vhdx) {
		return "fstrim on sparse data disk", nil
	}
	held, err := dockerHeld(ctx, r)
	if err != nil {
		return "", err
	}
	gain := float64(fi.Size()) - held
	if gain < reclaimMinGain {
		return "", nil
	}

	if _, err := r.Run(ctx, cmdTimeout, "docker", "desktop", "stop"); err != nil {
		r.Run(ctx, cmdTimeout, "taskkill", "/F", "/IM", "Docker Desktop.exe")
	}
	r.Run(ctx, cmdTimeout, "wsl", "--shutdown")
	compactErr := diskpart(ctx, r, compactScript(vhdx))
	if compactErr != nil {
		diskpart(ctx, r, selectVDisk(vhdx)+"detach vdisk\n") // never leave it attached
	}
	if err := relaunch(); err != nil {
		return "", fmt.Errorf("relaunch Docker Desktop after compact: %w", err)
	}
	if compactErr != nil {
		return "", fmt.Errorf("compact %s: %w", vhdx, compactErr)
	}
	after := fi.Size()
	if fi2, err := os.Stat(vhdx); err == nil {
		after = fi2.Size()
	}
	return fmt.Sprintf("compacted %s: %.1fGB -> %.1fGB", vhdx, float64(fi.Size())/1e9, float64(after)/1e9), nil
}

func diskpart(ctx context.Context, r execx.Runner, script string) error {
	f, err := os.CreateTemp("", "runner-guard-diskpart-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(script)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	_, err = r.Run(ctx, compactTimeout, "diskpart", "/s", f.Name())
	return err
}

func isSparse(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(p)
	return err == nil && attrs&0x200 != 0 // FILE_ATTRIBUTE_SPARSE_FILE
}

// selectVDisk quotes the path for diskpart. Not %q: Go escaping doubles
// every backslash, which diskpart takes literally.
func selectVDisk(vhdx string) string { return `select vdisk file="` + vhdx + "\"\n" }

func compactScript(vhdx string) string {
	return selectVDisk(vhdx) + "attach vdisk readonly\ncompact vdisk\ndetach vdisk\n"
}
