package simulation

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

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
	g := loadGrid(t)
	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.CongestionParams{Alpha: 1, Beta: 2},
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})
	car := engine.SpawnVehicle(0)
	car.SetDestination(2)
	if err := car.PlanRoute(); err != nil {
		t.Fatalf("plan route: %v", err)
	}
	const jammed = 1 // edge 1->2
	if len(car.Route) != 2 || car.Route[1].ID != jammed {
		t.Fatalf("want the direct route 0-1-2 first, got %d edges", len(car.Route))
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

// Stop must end Run while other goroutines flood it with pause and resume.
func TestStopEndsRunDuringPauseResume(t *testing.T) {
	g := loadGrid(t)

	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.DefaultCongestionParams(),
		TickRate:         100.0,
		SpeedMultiplier:  1.0,
	})

	for i := 0; i < 5; i++ {
		v := engine.SpawnVehicle(i)
		v.SetDestination(((i + 5) % 9) + 1)
		if err := v.PlanRoute(); err != nil {
			t.Fatalf("plan route failed: %v", err)
		}
	}

	runDone := make(chan struct{})
	go func() {
		engine.Run()
		close(runDone)
	}()

	var toggles sync.WaitGroup
	for w := 0; w < 4; w++ {
		toggles.Add(1)
		go func() {
			defer toggles.Done()
			for i := 0; i < 100; i++ {
				engine.Pause()
				engine.Resume()
			}
		}()
	}
	engine.Stop()
	defer toggles.Wait()

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of Stop")
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
