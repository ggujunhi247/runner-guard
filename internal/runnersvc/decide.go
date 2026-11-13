// Package runnersvc stops the local runner service while the host is unfit
// and starts it again once it recovers.
package runnersvc

import "time"

type Action int

const (
	None Action = iota
	Block
	Unblock
	ClearFlag
)

func (a Action) String() string {
	return [...]string{"none", "block", "unblock", "clear-flag"}[a]
}

// Why runner-guard stopped the runner; it decides how it is released.
const (
	ReasonDocker = "docker"
	ReasonDisk   = "disk"
)

const timeout = 60 * time.Second

// Decide applies the block/unblock rules. Only a service we stopped is
// started again. A disk block lifts below prunePct (not blockPct) so the
// runner doesn't flap around 90%; a Docker block lifts as soon as Docker
// answers and the disk is below blockPct.
func Decide(healthy bool, pct, prunePct, blockPct float64, running, blockedByUs bool, reason string) Action {
	if !healthy || pct >= blockPct {
		if running {
			return Block
		}
		return None
	}
	if !blockedByUs {
		return None
	}
	if running {
		return ClearFlag
	}
	if reason == ReasonDocker || pct < prunePct {
		return Unblock
	}
	return None
}
