package disk

import (
	"context"
	"reflect"
	"strings"
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
	{"docker", "network", "prune", "--force", "--filter", label},
}

func TestPruneDefaultSkipsImages(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages, c.PruneTestcontainers = false, false // Windows defaults them on
	if _, err := Prune(context.Background(), f, c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Calls, baseCalls) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestPruneImagesWhenEnabled(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages, c.PruneTestcontainers = true, false
	if _, err := Prune(context.Background(), f, c); err != nil {
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

const tc = "label=org.testcontainers=true"

func TestPruneTestcontainersOnlyWhenEnabled(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages, c.PruneTestcontainers = false, true
	if _, err := Prune(context.Background(), f, c); err != nil {
		t.Fatal(err)
	}
	want := append(append([][]string{}, baseCalls...),
		[]string{"docker", "container", "prune", "--force", "--filter", tc, "--filter", "until=1h"},
		[]string{"docker", "volume", "prune", "--all", "--force", "--filter", tc},
		[]string{"docker", "network", "prune", "--force", "--filter", tc, "--filter", "until=1h"})
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestPruneNeverUnfiltered(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages, c.PruneTestcontainers = true, true
	Prune(context.Background(), f, c)
	for _, call := range f.Calls {
		k := call[1]
		if (k == "container" || k == "volume" || k == "network") && !strings.Contains(strings.Join(call, " "), "--filter label=") {
			t.Errorf("unlabelled %v", call)
		}
	}
}

func TestPruneDeep(t *testing.T) {
	f := &execx.Fake{Responses: map[string]execx.FakeResponse{
		"docker builder prune --all --force": {Out: "Total:\t1GB\nTotal reclaimed space: 1.5GB\n"},
		"docker image prune --all --force":   {Out: "Total reclaimed space: 500MB\n"},
	}}
	c := config.Default()
	c.PruneImages, c.PruneDeep = true, true
	steps, err := PruneDeep(context.Background(), f, c)
	if err != nil || Total(steps) != 2e9 {
		t.Fatalf("%v %v", Total(steps), err)
	}
	want := [][]string{{"docker", "builder", "prune", "--all", "--force"}, {"docker", "image", "prune", "--all", "--force"}}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestPruneDeepSkippedWithoutPruneImages(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages = false
	steps, err := PruneDeep(context.Background(), f, c)
	if err != nil || len(steps) != 0 || len(f.Calls) != 0 {
		t.Fatalf("%v %v %v", steps, err, f.Calls)
	}
}

// prune_images alone (aged images only) never starts the deep pass.
func TestPruneDeepNeedsItsOwnSwitch(t *testing.T) {
	f := &execx.Fake{}
	c := config.Default()
	c.PruneImages, c.PruneDeep = true, false
	if steps, _ := PruneDeep(context.Background(), f, c); len(steps) != 0 || len(f.Calls) != 0 {
		t.Fatalf("deep pass ran: %v", f.Calls)
	}
}
