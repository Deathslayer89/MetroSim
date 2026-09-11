package dispatcher

import (
	"testing"
)

func TestDriverBehaviorInertByDefault(t *testing.T) {
	var b DriverBehavior
	if b.active() {
		t.Error("zero-value DriverBehavior should be inert")
	}
}

// A dispatcher with no behavior set accepts everything and counts nothing.
func TestAcceptsAlwaysWhenInert(t *testing.T) {
	d := &Dispatcher{}
	for i := 0; i < 100; i++ {
		if !d.accepts() {
			t.Fatal("inert dispatcher should accept every request")
		}
	}
	if s := d.BehaviorStats(); s.Declines != 0 {
		t.Errorf("no declines expected, got %d", s.Declines)
	}
}

// AcceptRate 0.5 declines about half, and the same seed gives the same count.
func TestAcceptRateDeclinesDeterministically(t *testing.T) {
	d := &Dispatcher{}
	d.SetDriverBehavior(DriverBehavior{AcceptRate: 0.5}, 42)

	accepted := 0
	const n = 1000
	for i := 0; i < n; i++ {
		if d.accepts() {
			accepted++
		}
	}
	if accepted < 400 || accepted > 600 {
		t.Errorf("AcceptRate=0.5 over %d: accepted %d, expected ~500", n, accepted)
	}
	if s := d.BehaviorStats(); s.Declines != n-accepted {
		t.Errorf("declines %d should equal rejected %d", s.Declines, n-accepted)
	}

	// Same seed, same sequence.
	d2 := &Dispatcher{}
	d2.SetDriverBehavior(DriverBehavior{AcceptRate: 0.5}, 42)
	accepted2 := 0
	for i := 0; i < n; i++ {
		if d2.accepts() {
			accepted2++
		}
	}
	if accepted != accepted2 {
		t.Errorf("same seed should give same accepts: %d vs %d", accepted, accepted2)
	}
}

func TestCancelRateDeterministic(t *testing.T) {
	d := &Dispatcher{}
	d.SetDriverBehavior(DriverBehavior{AcceptRate: 1.0, CancelRate: 0.1}, 7)
	cancels := 0
	const n = 1000
	for i := 0; i < n; i++ {
		if d.cancels() {
			cancels++
		}
	}
	if cancels < 50 || cancels > 160 {
		t.Errorf("CancelRate=0.1 over %d: %d cancels, expected ~100", n, cancels)
	}
	if s := d.BehaviorStats(); s.Cancellations != cancels {
		t.Errorf("cancellation count mismatch: stats=%d counted=%d", s.Cancellations, cancels)
	}

	d2 := &Dispatcher{}
	d2.SetDriverBehavior(DriverBehavior{AcceptRate: 1.0, CancelRate: 0.1}, 7)
	again := 0
	for i := 0; i < n; i++ {
		if d2.cancels() {
			again++
		}
	}
	if again != cancels {
		t.Errorf("same seed: %d cancels, then %d", cancels, again)
	}
}
