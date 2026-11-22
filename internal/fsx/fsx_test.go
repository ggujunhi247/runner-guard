package fsx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileReplacesAndLeavesNoTemp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	for _, body := range []string{"one", "two"} {
		if err := WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(p); string(b) != body {
			t.Fatalf("got %q", b)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".*rg-tmp*")); len(left) != 0 {
		t.Fatalf("temp file left: %v", left)
	}
}

// A pre-made temp file (someone else's) is never written into.
func TestWriteFileIgnoresPlantedTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	os.WriteFile(p+".rg-tmp", []byte("planted"), 0o666)
	if err := WriteFile(p, []byte("ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "ours" {
		t.Fatalf("got %q", b)
	}
}

// A symlink at path is replaced, never written through.
func TestWriteFileDoesNotFollowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("keep"), 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("wrote through the symlink: %q", b)
	}
}
