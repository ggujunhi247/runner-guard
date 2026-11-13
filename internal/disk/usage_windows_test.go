package disk

import "testing"

func TestHostUsedPctReadsRealDrive(t *testing.T) {
	pct, err := hostUsedPct(`C:\`)
	if err != nil || pct <= 0 || pct > 100 {
		t.Fatalf("got %v, %v", pct, err)
	}
}
