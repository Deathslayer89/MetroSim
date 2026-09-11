package simulation

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
)

// runWaits drives a whole engine end-to-end from a fixed seed and returns the
// per-completed-ride pickup waits, in completion order. Driver cancellation is
// on so the RNG-order path is exercised too.
func runWaits(t *testing.T, seed int64) []float64 {
	t.Helper()
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	sc := &scenario.Scenario{
		Name:      "determinism",
		Duration:  60 * time.Second,
		Seed:      seed,
		StartTime: time.Unix(0, 0),
		Vehicles:  scenario.VehicleConfig{Count: 10, Spawn: "random"},
		Arrivals: scenario.ArrivalConfig{RateSegments: []scenario.RatePoint{
			{T: 0, Rate: 1.0}, {T: 60 * time.Second, Rate: 1.0},
		}},
	}

	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})
	engine.GetDispatcher().SetPolicy(dispatcher.NewBatchPolicy(3 * time.Second))
	engine.GetDispatcher().SetDriverBehavior(dispatcher.DriverBehavior{CancelRate: 0.02}, seed)
	engine.SetStartTime(sc.StartTime)

	gen := scenario.NewGenerator(sc, g)
	engine.SetArrivalSource(gen)
	for _, n := range gen.VehicleSpawnNodes(g) {
		engine.SpawnVehicle(n)
	}

	const tickDt = time.Second / 10
	ticks := int((sc.Duration + sc.Duration/2) / tickDt)
	d := engine.GetDispatcher()
	for i := 0; i < ticks; i++ {
		engine.Tick()
	}

	completed := d.GetCompletedRides()
	waits := make([]float64, 0, len(completed))
	for _, r := range completed {
		waits = append(waits, r.PickupTime.Sub(r.Request.RequestTime).Seconds())
	}
	return waits
}

// Two full engine runs with the same seed must produce identical wait streams.
func TestEngineRunIsDeterministic(t *testing.T) {
	a := runWaits(t, 42)
	b := runWaits(t, 42)
	if len(a) == 0 {
		t.Fatal("no completed rides, nothing to compare")
	}
	if len(a) != len(b) {
		t.Fatalf("same seed produced different completion counts: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed diverged at ride %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// A different seed should change the outcome, or the test above proves nothing.
func TestEngineRunVariesWithSeed(t *testing.T) {
	a := runWaits(t, 1)
	b := runWaits(t, 2)
	same := len(a) == len(b)
	if same {
		for i := range a {
			if a[i] != b[i] {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("different seeds produced identical results, so the seed isn't used")
	}
}
