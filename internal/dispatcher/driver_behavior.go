package dispatcher

import (
	"math/rand"
)

// DriverBehavior models supply-side friction: declines and pre-pickup
// cancellation. Zero value is inert (always accept, never cancel) so it's
// opt-in. Randomness uses the dispatcher's seeded RNG.
type DriverBehavior struct {
	AcceptRate float64 // P(accept) in (0,1); <=0 or >=1 means always accept
	CancelRate float64 // per-tick P(cancel) while en route to pickup
}

// active reports whether any friction is configured, so the hot path can skip
// the RNG entirely. AcceptRate is a friction only in (0,1).
func (b DriverBehavior) active() bool {
	return (b.AcceptRate > 0 && b.AcceptRate < 1.0) || b.CancelRate > 0
}

// behaviorSeedSalt keeps the behavior stream apart from the arrival stream,
// which is seeded with the same run seed.
const behaviorSeedSalt = 0x5DEECE66D

// SetDriverBehavior installs supply-side behavior. Pass the run seed so
// replicates are reproducible.
func (d *Dispatcher) SetDriverBehavior(b DriverBehavior, seed int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.driverBehavior = b
	d.behaviorRNG = rand.New(rand.NewSource(seed ^ behaviorSeedSalt))
}

// accepts decides whether the offered driver takes the assignment. Caller holds
// d.mu. Counts declines for the metrics getter.
func (d *Dispatcher) accepts() bool {
	if !d.driverBehavior.active() || d.behaviorRNG == nil {
		return true
	}
	if d.driverBehavior.AcceptRate > 0 && d.driverBehavior.AcceptRate < 1.0 && d.behaviorRNG.Float64() > d.driverBehavior.AcceptRate {
		d.declines++
		return false
	}
	return true
}

// cancels decides whether an en-route driver bails this tick. Caller holds d.mu.
func (d *Dispatcher) cancels() bool {
	if d.driverBehavior.CancelRate <= 0 || d.behaviorRNG == nil {
		return false
	}
	if d.behaviorRNG.Float64() < d.driverBehavior.CancelRate {
		d.cancellations++
		return true
	}
	return false
}

// BehaviorStats is a snapshot of supply-side friction counts since startup.
type BehaviorStats struct {
	Declines      int
	Cancellations int
}

func (d *Dispatcher) BehaviorStats() BehaviorStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return BehaviorStats{Declines: d.declines, Cancellations: d.cancellations}
}
