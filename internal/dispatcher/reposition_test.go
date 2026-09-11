package dispatcher

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

var t0 = time.Unix(1_700_000_000, 0)

// repositionFixture has node 0 and node 1 about 1.1 km apart, in different r8
// cells, with a road each way.
func repositionFixture(t *testing.T) (*Dispatcher, *graph.Graph, *pathfinding.PathPlanner) {
	t.Helper()
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.7700, Lon: -122.4200})
	g.AddNode(&graph.Node{ID: 1, Lat: 37.7800, Lon: -122.4200})
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 1100, SpeedLimit: 10, Lanes: 1})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 1, ToNode: 0, Length: 1100, SpeedLimit: 10, Lanes: 1})
	planner := pathfinding.NewPathPlanner(g, nil)
	d := NewDispatcher(g, planner, events.NewMemoryBus(), events.NewStamper(events.RunInfo{}))
	d.SetRepositioning(Repositioning{IdleAfter: time.Minute, Every: 30 * time.Second, Window: 15 * time.Minute, Rings: 3})
	a, _ := d.cellOf(0)
	b, _ := d.cellOf(1)
	if a == b {
		t.Fatal("fixture nodes share a cell")
	}
	return d, g, planner
}

func TestRepositionWaitsForIdleAfterThenMovesTowardDemand(t *testing.T) {
	d, g, planner := repositionFixture(t)
	car := agent.NewVehicle(1, 0, g, planner)
	vehicles := map[int]*agent.Vehicle{1: car}
	for i := 0; i < 3; i++ {
		d.notePickup(&Request{PickupNode: 1, RequestTime: t0})
	}

	d.repositionIdle(vehicles, t0, nil)
	if car.State != agent.StateIdle {
		t.Fatalf("a car idle for 0 s moved: %s", car.State)
	}
	d.repositionIdle(vehicles, t0.Add(90*time.Second), nil)
	if car.State != agent.StateRepositioning || car.Destination != 1 {
		t.Fatalf("after 90 s idle: want repositioning to node 1, got %s to node %d", car.State, car.Destination)
	}
}

// One recent pickup is worth one car, not every idle car nearby.
func TestRepositionSendsOnlyAsManyCarsAsTheGap(t *testing.T) {
	d, g, planner := repositionFixture(t)
	vehicles := map[int]*agent.Vehicle{
		1: agent.NewVehicle(1, 0, g, planner),
		2: agent.NewVehicle(2, 0, g, planner),
	}
	d.notePickup(&Request{PickupNode: 1, RequestTime: t0})

	d.repositionIdle(vehicles, t0, nil)
	d.repositionIdle(vehicles, t0.Add(90*time.Second), nil)
	moved := 0
	for _, v := range vehicles {
		if v.State == agent.StateRepositioning {
			moved++
		}
	}
	if moved != 1 {
		t.Errorf("want 1 car sent toward one pickup, got %d", moved)
	}
}

// A repositioning car carries no rider, so it can still be matched.
func TestRepositioningCarCanBeMatched(t *testing.T) {
	d, g, planner := repositionFixture(t)
	car := agent.NewVehicle(1, 0, g, planner)
	vehicles := map[int]*agent.Vehicle{1: car}
	if err := car.Reposition(1, nil); err != nil {
		t.Fatalf("reposition: %v", err)
	}

	now := t0.Add(time.Minute)
	d.SubmitRequest(&Request{ID: 7, PickupNode: 1, DestinationNode: 0, RequestTime: now})
	d.Tick(vehicles, now, nil)
	if d.GetActiveRideCount() != 1 || car.State != agent.StateEnroute {
		t.Fatalf("want the repositioning car assigned, got %d active rides and state %s", d.GetActiveRideCount(), car.State)
	}
}
