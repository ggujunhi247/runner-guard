package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || !reflect.DeepEqual(c, Default()) {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestLoadOverridesOnlyGivenKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"prune_percent":80}`), 0o644)
	c, err := Load(p)
	if err != nil || c.PrunePercent != 80 || c.BlockPercent != 90 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestDurationsRejectsGarbage(t *testing.T) {
	c := Default()
	c.DockerRestartMinInterval = "soon"
	if _, _, err := c.Durations(); err == nil {
		t.Fatal("want error")
	}
}

func TestStuckAge(t *testing.T) {
	c := Default()
	if d, err := c.StuckAge(); d != 6*time.Hour || err != nil {
		t.Fatalf("default should be 6h: %v %v", d, err)
	}
	c.StuckContainerAge = ""
	if d, err := c.StuckAge(); d != 0 || err != nil {
		t.Fatalf("empty must turn it off: %v %v", d, err)
	}
	c.StuckContainerAge = "30m"
	if _, _, err := c.Durations(); err == nil {
		t.Fatal("30m must be rejected")
	}
	c.StuckContainerAge = "6h"
	if d, err := c.StuckAge(); d != 6*time.Hour || err != nil {
		t.Fatalf("got %v %v", d, err)
	}
}

func TestClampFixesUnusableThresholds(t *testing.T) {
	c := Default()
	if w := c.Clamp(); w != "" {
		t.Fatalf("defaults clamped: %s", w)
	}
	c.PrunePercent, c.BlockPercent = 92, 97
	if w := c.Clamp(); w == "" || c.BlockPercent != 95 || c.PrunePercent != 92 {
		t.Fatalf("%v %v %q", c.PrunePercent, c.BlockPercent, w)
	}
}

func TestClampPruneAboveBlock(t *testing.T) {
	c := Default()
	c.PrunePercent = 90
	if w := c.Clamp(); w == "" || c.PrunePercent != 80 {
		t.Fatal(c.PrunePercent, w)
	}
}

func TestLoadAcceptsBOM(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte("\xEF\xBB\xBF{\"block_percent\": 80}"), 0o644)
	if c, err := Load(p); err != nil || c.BlockPercent != 80 {
		t.Fatalf("%v %v", c.BlockPercent, err)
	}
	os.WriteFile(p, []byte{0xFF, 0xFE, '{', 0, '}', 0}, 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("%v", err)
	}
}

func TestPruneTestcontainersDefault(t *testing.T) {
	if got, want := Default().PruneTestcontainers, runtime.GOOS == "windows"; got != want {
		t.Fatalf("%v", got)
	}
}
