package config

import (
	"os"
	"path/filepath"
	"reflect"
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
