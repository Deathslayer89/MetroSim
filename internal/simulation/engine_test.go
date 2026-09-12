package simulation

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

func loadGrid(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load test grid: %v", err)
	}
	return g
}

// Fifty vehicles funneled toward node 9 must push some edge more than 10%
// above its free-flow travel time.
func TestFunneledVehiclesRaiseEdgeWeights(t *testing.T) {
	g := loadGrid(t)
	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.CongestionParams{Alpha: 1.0, Beta: 2.0},
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})
	for i := 0; i < 50; i++ {
		start := i % 9
		v := engine.SpawnVehicle(start)
		if start <= 4 {
			v.SetDestination(9)
			if err := v.PlanRoute(); err != nil {
				t.Fatalf("plan route from %d: %v", start, err)
			}
		}
	}

	slowest := 1.0
	for tick := 0; tick < 100; tick++ {
		engine.Tick()
		for id, w := range engine.GetTrafficModel().GetEdgeWeights() {
			slowest = max(slowest, w/g.Edges[id].BaseWeight)
		}
	}
	if slowest <= 1.1 {
		t.Errorf("want some edge slower than 1.1x free flow, slowest was %.2fx", slowest)
	}
}

// A pileup on the next edge of a car's route makes the engine send the car
// around it on the following replan pass.
func TestEngineReroutesAroundPileup(t *testing.T) {
	// 0 -> 1 -> 2 is the quick route; 1 -> 3 -> 2 is a 26 s detour. Jammed to
	// the 5x cap, edge 1 -> 2 takes 100 s, over three times the detour, so even
	// the weight-3 heuristic has to take the detour.
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.7700, Lon: -122.4200})
	g.AddNode(&graph.Node{ID: 1, Lat: 37.7710, Lon: -122.4200})
	g.AddNode(&graph.Node{ID: 2, Lat: 37.7730, Lon: -122.4200})
	g.AddNode(&graph.Node{ID: 3, Lat: 37.7720, Lon: -122.4210})
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 111, SpeedLimit: 13.9, Lanes: 1})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 1, ToNode: 2, Length: 222, SpeedLimit: 11.1, Lanes: 1})
	g.AddEdge(&graph.Edge{ID: 2, FromNode: 1, ToNode: 3, Length: 260, SpeedLimit: 20, Lanes: 1})
	g.AddEdge(&graph.Edge{ID: 3, FromNode: 3, ToNode: 2, Length: 260, SpeedLimit: 20, Lanes: 1})
	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})
	car := engine.SpawnVehicle(0)
	car.SetDestination(2)
	if err := car.PlanRoute(); err != nil {
		t.Fatalf("plan route: %v", err)
	}
	const jammed = 1
	if len(car.Route) != 2 || car.Route[1].ID != jammed {
		t.Fatalf("want the quick route 0-1-2 first, got %d edges", len(car.Route))
	}
	for i := 0; i < 100; i++ {
		v := engine.SpawnVehicle(1)
		v.SetDestination(2)
		if err := v.PlanRoute(); err != nil {
			t.Fatalf("plan route: %v", err)
		}
	}

	for tick := 0; tick <= replanInterval+1; tick++ {
		engine.Tick()
	}
	for _, e := range car.Route {
		if e.ID == jammed {
			t.Fatal("car still routes over the jammed edge 1->2")
		}
	}
}

// A car alone on an empty road drives at the speed limit, however finely OSM
// has cut the road up. Counted per segment, each 3 m piece was over capacity
// with the car on it, and the drive took four times as long.
func TestLoneCarDrivesAtFreeFlow(t *testing.T) {
	g := graph.NewGraph()
	for i := 0; i <= 50; i++ {
		g.AddNode(&graph.Node{ID: i, Lat: 37.77 + float64(i)*3/111_000, Lon: -122.42})
	}
	for i := 0; i < 50; i++ {
		g.AddEdge(&graph.Edge{ID: i, FromNode: i, ToNode: i + 1, Length: 3, SpeedLimit: 10, Lanes: 1})
	}
	engine := NewEngine(Config{Graph: g, CongestionParams: traffic.DemoCongestionParams(), TickRate: 10})
	car := engine.SpawnVehicle(0)
	car.SetDestination(50)
	if err := car.PlanRoute(); err != nil {
		t.Fatalf("plan route: %v", err)
	}
	ticks := 0
	for car.CurrentNode != 50 && ticks < 1000 {
		engine.Tick()
		ticks++
	}
	// 150 m at 10 m/s is 15 s, or 150 ticks; one more covers rounding.
	if ticks > 151 {
		t.Errorf("the drive took %.1f s; at the speed limit it takes 15 s", float64(ticks)/10)
	}
}

// GetVehicleSnapshots must not race Tick (GetVehicles hands out live pointers).
func TestGetVehicleSnapshotsRaceFree(t *testing.T) {
	g := loadGrid(t)

	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.DefaultCongestionParams(),
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})

	for i := 0; i < 10; i++ {
		v := engine.SpawnVehicle(i % 9)
		v.SetDestination(((i + 5) % 9) + 1)
		if err := v.PlanRoute(); err != nil {
			t.Fatalf("plan route failed: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			engine.Tick()
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		snaps := engine.GetVehicleSnapshots()
		if len(snaps) != 10 {
			t.Fatalf("expected 10 snapshots, got %d", len(snaps))
		}
	}
	<-done
}

// Stop has to end Run while it's paused, when Run waits on commands rather
// than ticks.
func TestStopEndsRunWhilePaused(t *testing.T) {
	engine := NewEngine(Config{
		Graph:            loadGrid(t),
		CongestionParams: traffic.DefaultCongestionParams(),
		TickRate:         100.0,
		SpeedMultiplier:  1.0,
	})
	runDone := make(chan struct{})
	go func() {
		engine.Run()
		close(runDone)
	}()

	engine.Pause()
	deadline := time.Now().Add(2 * time.Second)
	for engine.GetState() != StatePaused && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if engine.GetState() != StatePaused {
		t.Fatal("Run never paused")
	}
	engine.Stop()

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of Stop while paused")
	}
	if engine.GetState() != StateStopped {
		t.Errorf("expected StateStopped after Run exit, got %v", engine.GetState())
	}
}

// A speed that isn't finite and positive would freeze or reverse the clock, so
// the engine ignores it. Commands run in order, so once the pause that follows
// them lands, the bad speeds have been dealt with.
func TestSetSpeedIgnoresSpeedsThatStopOrReverseTime(t *testing.T) {
	engine := NewEngine(Config{Graph: loadGrid(t), CongestionParams: traffic.DemoCongestionParams(), TickRate: 100})
	done := make(chan struct{})
	go func() {
		engine.Run()
		close(done)
	}()
	defer func() {
		engine.Stop()
		<-done
	}()

	for _, bad := range []float64{-5, 0, math.NaN(), math.Inf(1)} {
		engine.SetSpeed(bad)
	}
	engine.Pause()
	deadline := time.Now().Add(2 * time.Second)
	for engine.GetState() != StatePaused && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if engine.GetState() != StatePaused {
		t.Fatal("the engine never paused")
	}
	if got := engine.GetSpeed(); got != 1 {
		t.Errorf("speed changed to %v", got)
	}
}

// Each driver's updates carry its own key, so they stay in order on one
// partition instead of being spread by the partitioner.
func TestDriverUpdatesAreKeyedByDriver(t *testing.T) {
	engine := NewEngine(Config{Graph: loadGrid(t), CongestionParams: traffic.DemoCongestionParams()})
	engine.SpawnVehicle(0)
	engine.SpawnVehicle(1)
	seen, wrong := 0, 0
	err := events.SubscribeDriverLocationUpdate(engine.Bus(), "test", func(e *eventspb.DriverLocationUpdate) {
		seen++
		if e.GetMeta().GetPartitionKey() != fmt.Sprintf("driver:%d", e.DriverId) {
			wrong++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.Tick()
	if seen != 2 || wrong != 0 {
		t.Errorf("want 2 updates keyed by driver, got %d updates, %d with the wrong key", seen, wrong)
	}
}

// Trip events go out after the tick releases the engine lock, so a subscriber
// may call back into the engine. Published under the lock, this deadlocked.
func TestTripSubscribersCanReadTheEngine(t *testing.T) {
	engine := NewEngine(Config{Graph: loadGrid(t), CongestionParams: traffic.DemoCongestionParams()})
	engine.SpawnVehicle(0)
	matched := make(chan time.Time, 1)
	err := events.SubscribeTripMatched(engine.Bus(), "test", func(*eventspb.TripMatched) {
		select {
		case matched <- engine.GetCurrentTime():
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.SubmitRequest(&dispatcher.Request{ID: 1, PickupNode: 3, DestinationNode: 8})

	done := make(chan struct{})
	go func() {
		engine.Tick()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Tick deadlocked on a subscriber that reads the engine")
	}
	select {
	case <-matched:
	default:
		t.Error("no TripMatched was published")
	}
}
