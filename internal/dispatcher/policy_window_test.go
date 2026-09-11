package dispatcher

import (
	"testing"
	"time"
)

// The window gate fires only once Window has elapsed since the last fire;
// time comes from ctx.Now, so no sleeping.
func TestBatchPolicyWindowGates(t *testing.T) {
	bp := NewBatchPolicy(3 * time.Second)
	base := time.Unix(1_700_000_000, 0)

	// Empty queue: Match returns nil either way, so lastFire is the observable.

	// First call always fires (lastFire is zero).
	bp.Match(MatchCtx{Now: base})
	if !bp.lastFire.Equal(base) {
		t.Fatalf("first call should advance lastFire to base, got %v", bp.lastFire)
	}

	// 1s later: under window, lastFire stays put.
	bp.Match(MatchCtx{Now: base.Add(1 * time.Second)})
	if !bp.lastFire.Equal(base) {
		t.Errorf("within window: lastFire should not advance, got %v", bp.lastFire)
	}

	// 2.9s later: still under window.
	bp.Match(MatchCtx{Now: base.Add(2900 * time.Millisecond)})
	if !bp.lastFire.Equal(base) {
		t.Errorf("just under window: lastFire should not advance, got %v", bp.lastFire)
	}

	// 3s later: at window boundary, fires.
	fireTime := base.Add(3 * time.Second)
	bp.Match(MatchCtx{Now: fireTime})
	if !bp.lastFire.Equal(fireTime) {
		t.Errorf("at window: lastFire should advance to %v, got %v", fireTime, bp.lastFire)
	}
}
