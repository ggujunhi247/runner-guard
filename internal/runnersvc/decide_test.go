package runnersvc

import "testing"

func TestDecideTable(t *testing.T) {
	cases := []struct {
		name                 string
		healthy              bool
		pct                  float64
		running, blockedByUs bool
		want                 Action
	}{
		{"disk full, running", true, 95, true, false, Block},
		{"docker dead, running, already flagged", false, 50, true, true, Block},
		{"unhealthy, stopped", false, 95, false, true, None},
		{"recovered below prune", true, 50, false, true, Unblock},
		{"hysteresis", true, 80, false, true, None},
		{"someone restarted it", true, 80, true, true, ClearFlag},
		{"healthy, not ours, stopped", true, 50, false, false, None},
		{"healthy, not ours, running", true, 50, true, false, None},
	}
	for _, c := range cases {
		if got := Decide(c.healthy, c.pct, 75, 90, c.running, c.blockedByUs); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
