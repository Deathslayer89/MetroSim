package dispatcher

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/metrics"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

func loadGrid(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load test grid: %v", err)
	}
	return g
}

// Ten drivers and five requests on the test grid: every request is matched on
// the first tick and every ride runs through pickup to dropoff.
func TestDispatcherCompletesAllRides(t *testing.T) {
	g := loadGrid(t)
	planner := pathfinding.NewPathPlanner(g, nil)
	bus := events.NewMemoryBus()
	collector := metrics.NewMetricsCollector()
	collector.SubscribeToBus(bus)
	d := NewDispatcher(g, planner, bus, events.NewStamper(events.RunInfo{}))

	vehicles := make(map[int]*agent.Vehicle)
	for i, node := range []int{0, 0, 1, 1, 2, 3, 6, 7, 8, 9} {
		v := agent.NewVehicle(i+1, node, g, planner)
		vehicles[v.ID] = v
	}
	now := time.Unix(1_700_000_000, 0)
	for i, r := range [][2]int{{1, 9}, {2, 8}, {3, 7}, {4, 6}, {5, 8}} {
		d.SubmitRequest(&Request{ID: i + 1, PickupNode: r[0], DestinationNode: r[1], RequestTime: now})
	}

	d.Tick(vehicles, now, nil)
	if p, a := d.GetPendingCount(), d.GetActiveRideCount(); p != 0 || a != 5 {
		t.Fatalf("after one tick: want 0 pending and 5 active, got %d and %d", p, a)
	}

	for tick := 0; tick < 200 && d.GetActiveRideCount() > 0; tick++ {
		now = now.Add(time.Second)
		for _, v := range vehicles {
			if v.IsEnroute() {
				v.Move(1)
			}
		}
		d.Tick(vehicles, now, nil)
	}

	if got := d.CompletedCount(); got != 5 {
		t.Fatalf("want 5 completed rides, got %d", got)
	}
	if got := collector.GetStats().TotalRides; got != 5 {
		t.Errorf("want 5 rides recorded by the collector, got %d", got)
	}
}

func TestH3DriverIndexInsertRemoveClear(t *testing.T) {
	idx := NewH3DriverIndex()

	idx.Insert(1, 37.7749, -122.4194)
	idx.Insert(2, 37.7750, -122.4194)
	idx.Insert(3, 37.7751, -122.4195)
	if idx.Count() != 3 {
		t.Errorf("Count after 3 inserts: want 3, got %d", idx.Count())
	}

	nearest := idx.NearestK(37.7749, -122.4194, 1)
	if len(nearest) != 1 || nearest[0].ID != 1 {
		t.Errorf("NearestK(exact-match, 1): want vehicle 1, got %+v", nearest)
	}

	if got := idx.NearestK(37.7750, -122.4194, 2); len(got) != 2 {
		t.Errorf("NearestK k=2: want 2 results, got %d", len(got))
	}

	idx.Remove(2)
	if idx.Count() != 2 {
		t.Errorf("Count after Remove(2): want 2, got %d", idx.Count())
	}

	idx.Clear()
	if idx.Count() != 0 {
		t.Errorf("Count after Clear: want 0, got %d", idx.Count())
	}
	if len(idx.CellsByDriverCount()) != 0 {
		t.Errorf("CellsByDriverCount after Clear: want empty, got %v", idx.CellsByDriverCount())
	}
}

// Drivers beyond candidateRings cells still come back through the exhaustive
// scan, so a pickup in a sparse area is not stranded.
func TestH3DriverIndexExhaustiveFallback(t *testing.T) {
	idx := NewH3DriverIndex()
	idx.Insert(7, 40.7128, -74.0060) // NYC, far outside the ring search

	got := idx.NearestK(37.7749, -122.4194, 1)
	if len(got) != 1 || got[0].ID != 7 {
		t.Errorf("expected NYC driver via fallback, got %+v", got)
	}
}

// CellsByDriverCount must reflect actual occupancy after insert/remove cycles.
func TestH3DriverIndexCellCounts(t *testing.T) {
	idx := NewH3DriverIndex()
	idx.Insert(1, 37.7749, -122.4194)
	idx.Insert(2, 37.7749, -122.4194) // same cell as 1
	idx.Insert(3, 37.8000, -122.4500) // different cell

	cells := idx.CellsByDriverCount()
	if len(cells) != 2 {
		t.Errorf("want 2 occupied cells, got %d: %v", len(cells), cells)
	}
	total := 0
	for _, c := range cells {
		total += c
	}
	if total != 3 {
		t.Errorf("want 3 total drivers across cells, got %d", total)
	}

	idx.Remove(1)
	cells = idx.CellsByDriverCount()
	if len(cells) != 2 {
		t.Errorf("after removing one driver from a 2-driver cell, want 2 cells, got %d", len(cells))
	}
	idx.Remove(2)
	cells = idx.CellsByDriverCount()
	if len(cells) != 1 {
		t.Errorf("after emptying first cell, want 1 cell remaining, got %d", len(cells))
	}
}

func TestGreedyMatching(t *testing.T) {
	g := loadGrid(t)

	planner := pathfinding.NewPathPlanner(g, nil)
	d := NewDispatcher(g, planner, events.NewMemoryBus(), events.NewStamper(events.RunInfo{}))

	vehicles := make(map[int]*agent.Vehicle)
	vehicles[1] = agent.NewVehicle(1, 0, g, planner) // next to the pickup at node 1
	vehicles[2] = agent.NewVehicle(2, 5, g, planner)
	vehicles[3] = agent.NewVehicle(3, 9, g, planner)

	req := &Request{
		ID:              1,
		PickupNode:      1,
		DestinationNode: 8,
		RequestTime:     time.Unix(1_700_000_000, 0),
	}
	d.SubmitRequest(req)

	d.Tick(vehicles, req.RequestTime, nil)

	if req.AssignedDriver != 1 {
		t.Errorf("want vehicle 1 assigned, got %d", req.AssignedDriver)
	}
	if !vehicles[1].IsEnroute() || vehicles[1].Destination != 1 {
		t.Errorf("want vehicle 1 enroute to node 1, got %s to node %d", vehicles[1].State, vehicles[1].Destination)
	}
}

// A completed ride produces exactly one MetricsCollector record.
func TestRideCompletionRecordsMetric(t *testing.T) {
	g := loadGrid(t)

	planner := pathfinding.NewPathPlanner(g, nil)
	bus := events.NewMemoryBus()
	mc := metrics.NewMetricsCollector()
	mc.SubscribeToBus(bus)
	d := NewDispatcher(g, planner, bus, events.NewStamper(events.RunInfo{}))

	vehicles := map[int]*agent.Vehicle{
		1: agent.NewVehicle(1, 0, g, planner),
	}

	requestTime := time.Unix(1_700_000_000, 0)
	req := &Request{
		ID:              42,
		PickupNode:      3,
		DestinationNode: 9,
		RequestTime:     requestTime,
	}
	d.SubmitRequest(req)

	currentTime := requestTime.Add(time.Second)
	d.Tick(vehicles, currentTime, nil)

	if d.GetActiveRideCount() != 1 {
		t.Fatalf("expected 1 active ride after match, got %d", d.GetActiveRideCount())
	}

	const deltaTime = 1.0
	for tick := 0; tick < 500; tick++ {
		currentTime = currentTime.Add(time.Second)
		for _, v := range vehicles {
			if v.IsEnroute() {
				v.Move(deltaTime)
			}
		}
		d.Tick(vehicles, currentTime, nil)
		if d.GetActiveRideCount() == 0 {
			break
		}
	}

	if d.GetActiveRideCount() != 0 {
		t.Fatalf("ride did not complete within tick budget")
	}

	if got := mc.GetRideCount(); got != 1 {
		t.Fatalf("expected exactly 1 recorded ride, got %d", got)
	}

	rec := mc.GetRides()[0]
	if rec.RideID == 0 {
		t.Errorf("RideID should be set, got 0")
	}

	if rec.AssignmentTime.Before(rec.RequestTime) {
		t.Errorf("assignment %v before request %v", rec.AssignmentTime, rec.RequestTime)
	}
	if rec.PickupTime.Before(rec.AssignmentTime) {
		t.Errorf("pickup %v before assignment %v", rec.PickupTime, rec.AssignmentTime)
	}
	if rec.DropoffTime.Before(rec.PickupTime) {
		t.Errorf("dropoff %v before pickup %v", rec.DropoffTime, rec.PickupTime)
	}

	if rec.WaitTime <= 0 {
		t.Errorf("WaitTime should be positive, got %v", rec.WaitTime)
	}
	if rec.TripDuration <= 0 {
		t.Errorf("TripDuration should be positive, got %v", rec.TripDuration)
	}
}
