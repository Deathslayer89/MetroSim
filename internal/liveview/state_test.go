package liveview

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
	"github.com/Deathslayer89/MetroSim/internal/simulation"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
)

// Riders giving up and drivers cancelling both change the queue, so the counts
// rebuilt from events have to match the dispatcher's after every tick.
func TestCountersMatchDispatcher(t *testing.T) {
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	sc := &scenario.Scenario{
		Name:      "liveview",
		Duration:  120 * time.Second,
		Seed:      3,
		StartTime: time.Unix(0, 0),
		Vehicles:  scenario.VehicleConfig{Count: 4, Spawn: "random"},
		Arrivals: scenario.ArrivalConfig{RateSegments: []scenario.RatePoint{
			{T: 0, Rate: 0.5}, {T: 120 * time.Second, Rate: 0.5},
		}},
	}
	engine := simulation.NewEngine(simulation.Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})
	d := engine.GetDispatcher()
	d.SetMaxWait(20 * time.Second)
	d.SetDriverBehavior(dispatcher.DriverBehavior{CancelRate: 0.002}, sc.Seed)
	engine.SetStartTime(sc.StartTime)

	state := NewState()
	if err := state.SubscribeToBus(engine.Bus()); err != nil {
		t.Fatal(err)
	}
	gen := scenario.NewGenerator(sc, g)
	engine.SetArrivalSource(gen)
	for _, n := range gen.VehicleSpawnNodes(g) {
		engine.SpawnVehicle(n)
	}

	for i := 0; i < 1200; i++ {
		engine.Tick()
		c := state.Snapshot().Counters
		if c.Pending != d.GetPendingCount() || c.Active != d.GetActiveRideCount() {
			t.Fatalf("tick %d: live view has %d pending, %d active; dispatcher has %d, %d",
				i, c.Pending, c.Active, d.GetPendingCount(), d.GetActiveRideCount())
		}
	}

	c := state.Snapshot().Counters
	if c.Abandoned == 0 || c.Cancelled == 0 {
		t.Fatalf("want both abandonments and cancellations, got %d and %d", c.Abandoned, c.Cancelled)
	}
	if c.Abandoned != d.AbandonedCount() || c.Cancelled != d.BehaviorStats().Cancellations {
		t.Errorf("live view counted %d abandoned, %d cancelled; dispatcher %d, %d",
			c.Abandoned, c.Cancelled, d.AbandonedCount(), d.BehaviorStats().Cancellations)
	}
}
