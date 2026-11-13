package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
