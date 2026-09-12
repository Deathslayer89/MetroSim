package liveview

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

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
	gen, err := scenario.NewGenerator(sc, g)
	if err != nil {
		t.Fatalf("generator: %v", err)
	}
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

type groupRecorder struct{ groups []string }

func (g *groupRecorder) Publish(string, proto.Message) error { return nil }
func (g *groupRecorder) Subscribe(_, group string, _ func(proto.Message)) error {
	g.groups = append(g.groups, group)
	return nil
}
func (g *groupRecorder) SubscribeBatched(_, group string, _ func(proto.Message), _ func() error) error {
	g.groups = append(g.groups, group)
	return nil
}

// live-view keeps its counts in memory, so each launch has to read every topic
// from the start rather than resume from a committed group offset.
func TestLiveViewSubscribesWithoutAGroup(t *testing.T) {
	var rec groupRecorder
	if err := NewState().SubscribeToBus(&rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.groups) == 0 {
		t.Fatal("no subscriptions")
	}
	for _, g := range rec.groups {
		if g != "" {
			t.Errorf("subscribed with group %q; a restart would resume mid-stream with empty counts", g)
		}
	}
}
