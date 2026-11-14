package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// Runs real diskpart on a throwaway 1GB VHDX; needs an elevated shell.
// RG_DISKPART_REAL=1 go test -run TestDiskpartCompactsRealVHDX -v ./internal/disk/
func TestDiskpartCompactsRealVHDX(t *testing.T) {
	if os.Getenv("RG_DISKPART_REAL") == "" {
		t.Skip("set RG_DISKPART_REAL=1 in an elevated shell")
	}
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "dummy.vhdx")
	ctx := context.Background()
	if err := diskpart(ctx, execx.OS{}, `create vdisk file="`+p+"\" maximum=1024 type=expandable\n"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := diskpart(ctx, execx.OS{}, compactScript(p)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Detached again: a second attach must work.
	if err := diskpart(ctx, execx.OS{}, selectVDisk(p)+"attach vdisk readonly\ndetach vdisk\n"); err != nil {
		t.Fatalf("left attached: %v", err)
	}
}
