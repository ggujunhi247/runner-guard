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

const timeout = 60 * time.Second

// Decide applies the block/unblock rules. Unblocking needs pct below
// prunePct (not blockPct) so the runner doesn't flap around 90%. Only a
// service we stopped is started again.
func Decide(healthy bool, pct, prunePct, blockPct float64, running, blockedByUs bool) Action {
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
	if pct < prunePct {
		return Unblock
	}
	return None
}
