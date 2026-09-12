package dispatcher

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

func ridesHeldBy(d *Dispatcher, driverID int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, r := range d.activeRides {
		if r.Driver.ID == driverID {
			n++
		}
	}
	return n
}

// A dropoff at the pickup plans an empty route, so the car stays Idle with its
// rider aboard. It must not take a second ride until the first one ends.
func TestDropoffAtPickupDoesNotDoubleBook(t *testing.T) {
	g := loadGrid(t)
	planner := pathfinding.NewPathPlanner(g, nil)
	d := NewDispatcher(g, planner, events.NewMemoryBus(), events.NewStamper(events.RunInfo{}))
	car := agent.NewVehicle(1, 1, g, planner)
	vehicles := map[int]*agent.Vehicle{1: car}

	now := t0
	d.SubmitRequest(&Request{ID: 1, PickupNode: 1, DestinationNode: 1, RequestTime: now})
	d.Tick(vehicles, now, nil)
	d.SubmitRequest(&Request{ID: 2, PickupNode: 5, DestinationNode: 8, RequestTime: now})
	for i := 0; i < 600 && len(d.GetCompletedRides()) < 2; i++ {
		now = now.Add(time.Second)
		car.Move(1)
		d.Tick(vehicles, now, nil)
		if n := ridesHeldBy(d, 1); n > 1 {
			t.Fatalf("after %s driver 1 holds %d rides", now.Sub(t0), n)
		}
	}
	if got := len(d.GetCompletedRides()); got != 2 {
		t.Errorf("want both rides completed, got %d with %d still active", got, d.GetActiveRideCount())
	}
}

// A car matched while standing on the pickup stays Idle until the next tick
// picks the rider up. Repositioning in between must leave it alone.
func TestCarMatchedOnItsPickupIsNotRepositioned(t *testing.T) {
	d, g, planner := repositionFixture(t)
	car := agent.NewVehicle(1, 0, g, planner)
	vehicles := map[int]*agent.Vehicle{1: car}
	for i := 0; i < 3; i++ {
		d.notePickup(&Request{PickupNode: 1, RequestTime: t0})
	}
	d.Tick(vehicles, t0, nil)

	now := t0.Add(90 * time.Second)
	d.SubmitRequest(&Request{ID: 1, PickupNode: 0, DestinationNode: 1, RequestTime: now})
	d.Tick(vehicles, now, nil)
	if car.State == agent.StateRepositioning {
		t.Fatal("the car matched on its pickup was sent to reposition")
	}
	d.Tick(vehicles, now.Add(100*time.Millisecond), nil)
	if car.State != agent.StateEnroute || car.Destination != 1 {
		t.Errorf("want the rider picked up and the car heading to node 1, got %s to node %d", car.State, car.Destination)
	}
}
