package dispatcher

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// A request with no drivers available abandons the queue once it waits past
// maxWait, and stays counted; with maxWait=0 it waits forever.
func TestRequestAbandonsAfterMaxWait(t *testing.T) {
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)
	bus := events.NewMemoryBus()
	d := NewDispatcher(g, planner, bus, events.NewStamper(events.RunInfo{}))
	d.SetMaxWait(60 * time.Second)
	var gaveUp []*eventspb.TripAbandoned
	if err := events.SubscribeTripAbandoned(bus, "test", func(e *eventspb.TripAbandoned) { gaveUp = append(gaveUp, e) }); err != nil {
		t.Fatal(err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	d.SubmitRequest(&Request{ID: 1, PickupNode: 1, DestinationNode: 8, RequestTime: t0})

	noDrivers := map[int]*agent.Vehicle{}

	// 30s in: still within the wait window, request is pending, none abandoned.
	d.Tick(noDrivers, t0.Add(30*time.Second), nil)
	if d.GetPendingCount() != 1 || d.AbandonedCount() != 0 {
		t.Fatalf("at 30s want pending=1 abandoned=0, got pending=%d abandoned=%d", d.GetPendingCount(), d.AbandonedCount())
	}

	// 90s in: past maxWait, so the rider gives up.
	d.Tick(noDrivers, t0.Add(90*time.Second), nil)
	if d.GetPendingCount() != 0 || d.AbandonedCount() != 1 {
		t.Errorf("at 90s want pending=0 abandoned=1, got pending=%d abandoned=%d", d.GetPendingCount(), d.AbandonedCount())
	}
	if len(gaveUp) != 1 || gaveUp[0].RequestId != 1 || gaveUp[0].WaitedS != 90 {
		t.Errorf("want one TripAbandoned for request 1 after 90 s, got %v", gaveUp)
	}
}

func TestNoAbandonWhenDisabled(t *testing.T) {
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)
	d := NewDispatcher(g, planner, events.NewMemoryBus(), events.NewStamper(events.RunInfo{}))
	// maxWait defaults to 0 (disabled).

	t0 := time.Unix(1_700_000_000, 0)
	d.SubmitRequest(&Request{ID: 1, PickupNode: 1, DestinationNode: 8, RequestTime: t0})
	d.Tick(map[int]*agent.Vehicle{}, t0.Add(time.Hour), nil)
	if d.GetPendingCount() != 1 || d.AbandonedCount() != 0 {
		t.Errorf("disabled: want pending=1 abandoned=0, got pending=%d abandoned=%d", d.GetPendingCount(), d.AbandonedCount())
	}
}
