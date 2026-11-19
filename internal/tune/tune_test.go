package tune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/state"
)

var t0 = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// hist makes 5-minute samples over days; f edits each one.
func hist(days float64, f func(i int, s *Sample)) []Sample {
	n := int(days * 24 * 12)
	h := make([]Sample, n)
	for i := range h {
		h[i] = Sample{T: t0.Add(time.Duration(i) * Interval), Concurrent: 2, Jobs: 2,
			MemTotalMB: 32000, MemAvailMB: 20000, JobMemMB: []float64{2000, 2000}, OtherMemMB: 1000}
		if f != nil {
			f(i, &h[i])
		}
	}
	return h
}

func in(h []Sample, st state.State) Input {
	return Input{History: h, State: st, Cfg: config.Default(), Now: h[len(h)-1].T.Add(time.Minute), Cores: 8}
}

func only(t *testing.T, p Plan, knob string) Change {
	t.Helper()
	for _, c := range p.Changes {
		if c.Knob == knob {
			return c
		}
	}
	t.Fatalf("no %s change: %+v", knob, p)
	return Change{}
}

func TestNoChangeBeforeGate(t *testing.T) {
	p := Decide(in(hist(5, nil), state.State{}))
	if len(p.Changes) != 0 || !strings.Contains(strings.Join(p.Notes, ";"), "collecting") {
		t.Fatalf("%+v", p)
	}
}

func TestRaiseWhenSaturated(t *testing.T) {
	c := only(t, Decide(in(hist(15, nil), state.State{})), Concurrent)
	if c.From != 2 || c.To != 3 || c.Revert {
		t.Fatalf("%+v", c)
	}
}

func TestNoRaiseWhenIdle(t *testing.T) {
	p := Decide(in(hist(15, func(i int, s *Sample) { s.Jobs = i % 2 }), state.State{}))
	if len(p.Changes) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestRaiseCappedByMemory(t *testing.T) {
	// (32000 - 1000 - 2048) / 14000 = 2 jobs fit; already 2.
	p := Decide(in(hist(15, func(_ int, s *Sample) { s.JobMemMB = []float64{14000, 14000} }), state.State{}))
	if len(p.Changes) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestStressAfterRaiseReverts(t *testing.T) {
	h := hist(15, func(i int, s *Sample) {
		s.Concurrent = 3
		if i > 4300 { // an OOM kill in the last hours
			s.OOMKill = 1
		}
	})
	st := state.State{Concurrent: state.Knob{Value: 3, Prev: 2, Raised: true, Since: h[len(h)-1].T.Add(-24 * time.Hour)}}
	c := only(t, Decide(in(h, st)), Concurrent)
	if c.To != 2 || !c.Revert {
		t.Fatalf("%+v", c)
	}
	k := st.Concurrent
	Record(&k, c, t0)
	if k.Ceiling != 3 || k.Reverts != 1 || k.Frozen {
		t.Fatalf("%+v", k)
	}
	Record(&k, c, t0)
	if !k.Frozen {
		t.Fatal("not frozen after 2 reverts")
	}
}

func TestStressWithoutJobsIsIgnored(t *testing.T) {
	h := hist(15, func(i int, s *Sample) { s.Jobs, s.MemAvailMB = 0, 100 })
	if p := Decide(in(h, state.State{})); len(p.Changes) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestSkippedSamplesDoNotCount(t *testing.T) {
	h := hist(15, func(i int, s *Sample) { s.Skip = "blocked" })
	if p := Decide(in(h, state.State{})); len(p.Changes) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestPruneLowersWhenDiskGrowsFast(t *testing.T) {
	var h []Sample
	for i := 0; i < 24*10; i++ { // 6 points per hour, pruned back each run
		h = append(h, Sample{T: t0.Add(time.Duration(i) * time.Hour), Kind: "run", Disk: 76, DiskEnd: 70})
	}
	c := only(t, Decide(in(h, state.State{})), Prune)
	if c.From != 75 || c.To != 73 {
		t.Fatalf("%+v", c)
	}
	st := state.State{PrunePercent: state.Knob{Value: 60, Since: t0}}
	for _, c := range Decide(in(h, st)).Changes {
		if c.Knob == Prune {
			t.Fatalf("went below the floor: %+v", c)
		}
	}
}

func TestWSLRaiseOnStress(t *testing.T) {
	h := hist(15, func(i int, s *Sample) {
		s.MemTotalMB, s.MemAvailMB = 8192, 300
		s.HostMemMB, s.HostMemPct = 32768, 60
		s.JobMemMB = []float64{1000, 1000}
	})
	x := in(h, state.State{})
	x.Windows = true
	c := only(t, Decide(x), WSLMemory)
	if c.From != 8 || c.To != 10 {
		t.Fatalf("%+v", c)
	}
	if p := Decide(x); p.Changes[0].Knob != WSLMemory {
		t.Fatalf("WSL raise should come before cutting concurrent: %+v", p.Changes)
	}
}

func TestConcurrentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "concurrent = 4\r\ncheck_interval = 0\r\n\r\n[[runners]]\r\n  name = \"x\"\r\n  token = \"keep\"\r\n  concurrent = 9\r\n"
	os.WriteFile(path, []byte(body), 0o600)
	if n, err := ReadConcurrent(path); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if err := SetConcurrent(path, 5, 3); err == nil {
		t.Fatal("must refuse when the value changed by hand")
	}
	if err := SetConcurrent(path, 4, 3); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if want := strings.Replace(body, "concurrent = 4", "concurrent = 3", 1); string(b) != want {
		t.Fatalf("got %q", b)
	}
}

func TestSetWSL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "[wsl2]\nmemory=8GB\n"},
		{"[wsl2]\nmemory=4GB\nswap=2GB\n", "[wsl2]\nmemory=8GB\nswap=2GB\n"},
		{"[wsl2]\nprocessors=4\n\n[experimental]\nx=1\n", "[wsl2]\nprocessors=4\n\nmemory=8GB\n[experimental]\nx=1\n"},
		{"[experimental]\nx=1\n", "[experimental]\nx=1\n[wsl2]\nmemory=8GB\n"},
	}
	for _, c := range cases {
		if got, err := SetWSL([]byte(c.in), "memory", 8); err != nil || string(got) != c.want {
			t.Errorf("SetWSL(%q) = %q %v, want %q", c.in, got, err, c.want)
		}
	}
	if got, _ := SetWSL([]byte("\xEF\xBB\xBF[wsl2]\nswap=2GB\n"), "memory", 8); string(got) != "[wsl2]\nswap=2GB\nmemory=8GB\n" {
		t.Errorf("BOM: %q", got)
	}
	if _, err := SetWSL([]byte("\xFF\xFE[\x00w\x00"), "memory", 8); err == nil {
		t.Error("UTF-16 must be refused")
	}
}

func TestParsers(t *testing.T) {
	ids := parseJobIDs("aaa 101\nbbb 101\nccc\n")
	if ids["aaa"] != "101" || ids["ccc"] != "" || len(ids) != 3 {
		t.Fatal(ids)
	}
	st := parseStats("aaa 512MiB / 7.6GiB\nbbb 1.5GiB / 7.6GiB\n")
	if st["aaa"] != 512 || st["bbb"] != 1536 {
		t.Fatal(st)
	}
	kv := parseKV("MemTotal:  1024 kB\npswpin 7\n")
	if kv["MemTotal"] != 1024 || kv["pswpin"] != 7 {
		t.Fatal(kv)
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	Append(path, Sample{T: t0, Jobs: 1})
	Append(path, Sample{T: t0.Add(time.Hour), Jobs: 2})
	os.WriteFile(path+".x", nil, 0o644)
	h, err := Load(path, t0)
	if err != nil || len(h) != 1 || h[0].Jobs != 2 {
		t.Fatal(h, err)
	}
	Rewrite(path, h)
	if h, _ := Load(path, time.Time{}); len(h) != 1 {
		t.Fatal(h)
	}
}

func TestMissingMemoryReadIsNotStress(t *testing.T) {
	h := hist(15, func(i int, s *Sample) {
		s.Pswpin, s.OOMKill = 1e6, 3 // lifetime counters
		if i%50 == 0 {
			s.MemTotalMB, s.MemAvailMB, s.Pswpin, s.OOMKill = 0, 0, 0, 0
		}
	})
	for _, c := range Decide(in(h, state.State{})).Changes {
		if c.To < c.From {
			t.Fatalf("lowered on a failed read: %+v", c)
		}
	}
}
