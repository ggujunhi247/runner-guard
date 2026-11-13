package disk

import (
	"context"
	"reflect"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]float64{"0B": 0, "512kB": 512e3, "1.5GB": 1.5e9, " 2TB ": 2e12} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseSize("12 bananas"); err == nil {
		t.Error("want error for bananas")
	}
}

func TestShouldPrune(t *testing.T) {
	cases := []struct {
		pct, th float64
		force   bool
		want    bool
	}{{80, 75, false, true}, {75, 75, false, true}, {74.9, 75, false, false}, {10, 75, true, true}}
	for _, c := range cases {
		if got := ShouldPrune(c.pct, c.th, c.force); got != c.want {
			t.Errorf("ShouldPrune(%v,%v,%v) = %v", c.pct, c.th, c.force, got)
		}
	}
}

const label = "label=com.gitlab.gitlab-runner.managed=true"

var baseCalls = [][]string{
	{"docker", "container", "prune", "--force", "--filter", label},
	{"docker", "volume", "prune", "--all", "--force", "--filter", label},
}

func TestPruneDefaultSkipsImages(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages = false // Windows defaults it on
	if err := Prune(context.Background(), f, c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Calls, baseCalls) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestPruneImagesWhenEnabled(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages = true
	if err := Prune(context.Background(), f, c); err != nil {
		t.Fatal(err)
	}
	want := append(append([][]string{}, baseCalls...),
		[]string{"docker", "image", "prune", "--all", "--force", "--filter", "until=72h"},
		[]string{"docker", "builder", "prune", "--force", "--filter", "until=72h"})
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestDesktopUsageTakesMax(t *testing.T) {
	if got := desktopPercent(30e9, 60e9, 20); got != 50 {
		t.Errorf("got %v", got)
	}
	if got := desktopPercent(6e9, 60e9, 92); got != 92 {
		t.Errorf("got %v", got)
	}
}

func TestSumDF(t *testing.T) {
	out := `{"Type":"Images","Size":"1.5GB"}
{"Type":"Containers","Size":"500MB"}
`
	got, err := sumDF(out)
	if err != nil || got != 2e9 {
		t.Fatalf("got %v, %v", got, err)
	}
}
