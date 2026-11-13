package dockerctl

import (
	"testing"
	"time"
)

func TestCanRestart(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	cases := []struct {
		name string
		h    []time.Time
		want bool
	}{
		{"empty", nil, true},
		{"too soon", []time.Time{ago(10 * time.Minute)}, false},
		{"cap reached", []time.Time{ago(time.Hour), ago(2 * time.Hour), ago(3 * time.Hour)}, false},
		{"old history ignored", []time.Time{ago(25 * time.Hour), ago(26 * time.Hour), ago(27 * time.Hour)}, true},
	}
	for _, c := range cases {
		if got := CanRestart(now, c.h, 30*time.Minute, 3); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestTrim(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	keep := now.Add(-time.Hour)
	got := Trim(now, []time.Time{now.Add(-25 * time.Hour), keep})
	if len(got) != 1 || !got[0].Equal(keep) {
		t.Fatalf("got %v", got)
	}
}
