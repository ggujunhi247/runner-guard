package runnersvc

import "testing"

func TestDecideTable(t *testing.T) {
	cases := []struct {
		name                 string
		healthy              bool
		pct                  float64
		running, blockedByUs bool
		reason               string
		want                 Action
	}{
		{"disk full, running", true, 95, true, false, "", Block},
		{"docker dead, running, already flagged", false, 50, true, true, ReasonDocker, Block},
		{"unhealthy, stopped", false, 95, false, true, ReasonDisk, None},
		{"disk block recovered below prune", true, 50, false, true, ReasonDisk, Unblock},
		{"disk block hysteresis", true, 80, false, true, ReasonDisk, None},
		{"docker block lifts once docker answers", true, 80, false, true, ReasonDocker, Unblock},
		{"someone restarted it", true, 80, true, true, ReasonDisk, ClearFlag},
		{"healthy, not ours, stopped", true, 50, false, false, "", None},
		{"healthy, not ours, running", true, 50, true, false, "", None},
	}
	for _, c := range cases {
		if got := Decide(c.healthy, c.pct, 75, 90, c.running, c.blockedByUs, c.reason); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestTuneStopLiftsLikeDocker(t *testing.T) {
	if Decide(true, 80, 75, 90, false, true, ReasonTune) != Unblock {
		t.Fatal("tune stop must lift once Docker answers and disk < block")
	}
}
