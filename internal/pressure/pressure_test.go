package pressure

import (
	"testing"
	"time"
)

func TestParseMeminfo(t *testing.T) {
	got, err := parseMeminfo("MemTotal:       1000 kB\nMemFree:  100 kB\nMemAvailable:    250 kB\n")
	if err != nil || got != 75 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseMeminfo("garbage"); err == nil {
		t.Fatal("want error")
	}
}

func TestParseLoadavg(t *testing.T) {
	got, err := parseLoadavg("8.00 4.00 2.00 3/400 1234\n", 4)
	if err != nil || got != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestReasons(t *testing.T) {
	if r := Reasons(Sample{MemPct: 50, LoadPerCPU: -1}, time.Second); len(r) != 0 {
		t.Fatalf("healthy host flagged: %v", r)
	}
	if r := Reasons(Sample{MemPct: 95, LoadPerCPU: 3}, 30*time.Second); len(r) != 3 {
		t.Fatalf("got %v", r)
	}
}

func TestReadWorksHere(t *testing.T) {
	s, err := Read()
	if err != nil || s.MemPct <= 0 || s.MemPct > 100 {
		t.Fatalf("got %+v, %v", s, err)
	}
}
