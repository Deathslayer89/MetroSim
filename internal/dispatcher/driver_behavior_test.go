package dispatcher

import (
	"strings"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
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

// A cancelled request can be matched again in the same tick, so its
// TripCancelled has to go out before the new TripMatched.
func TestCancelPublishedBeforeRematch(t *testing.T) {
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)
	bus := events.NewMemoryBus()
	d := NewDispatcher(g, planner, bus, events.NewStamper(events.RunInfo{}))
	d.SetDriverBehavior(DriverBehavior{CancelRate: 1}, 1)

	var order []string
	var matched []*eventspb.TripMatched
	var cancelled []*eventspb.TripCancelled
	if err := events.SubscribeTripMatched(bus, "test", func(e *eventspb.TripMatched) {
		order = append(order, "matched")
		matched = append(matched, e)
	}); err != nil {
		t.Fatal(err)
	}
	if err := events.SubscribeTripCancelled(bus, "test", func(e *eventspb.TripCancelled) {
		order = append(order, "cancelled")
		cancelled = append(cancelled, e)
	}); err != nil {
		t.Fatal(err)
	}

	vehicles := map[int]*agent.Vehicle{1: agent.NewVehicle(1, 0, g, planner)}
	t0 := time.Unix(1_700_000_000, 0)
	d.SubmitRequest(&Request{ID: 7, PickupNode: 1, DestinationNode: 8, RequestTime: t0})
	d.Tick(vehicles, t0, nil)
	d.Tick(vehicles, t0.Add(100*time.Millisecond), nil)

	if got := strings.Join(order, ","); got != "matched,cancelled,matched" {
		t.Fatalf("events %q, want matched,cancelled,matched", got)
	}
	if c := cancelled[0]; c.RideId != matched[0].RideId || c.RequestId != 7 || c.DriverId != 1 {
		t.Errorf("cancel %v doesn't describe the first ride %v", c, matched[0])
	}
	if matched[1].RideId == matched[0].RideId || matched[1].RequestId != 7 {
		t.Errorf("want request 7 rematched under a new ride, got %v", matched[1])
	}
}
