package simulation

import (
	"math"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
)

// streetGrid is an n×n grid of two-way streets spaced meters apart. At 300 m a
// 10×10 grid spans many H3 r8 cells, so repositioning has somewhere to go.
func streetGrid(n int, spacing float64) *graph.Graph {
	const lat0, lon0 = 37.76, -122.44
	dLat := spacing / 111_000
	dLon := dLat / math.Cos(lat0*math.Pi/180)
	g := graph.NewGraph()
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			g.AddNode(&graph.Node{ID: r*n + c, Lat: lat0 + float64(r)*dLat, Lon: lon0 + float64(c)*dLon})
		}
	}
	id := 0
	street := func(a, b int) {
		g.AddEdge(&graph.Edge{ID: id, FromNode: a, ToNode: b, Length: spacing, SpeedLimit: 11, Lanes: 1})
		g.AddEdge(&graph.Edge{ID: id + 1, FromNode: b, ToNode: a, Length: spacing, SpeedLimit: 11, Lanes: 1})
		id += 2
	}
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if c+1 < n {
				street(r*n+c, r*n+c+1)
			}
			if r+1 < n {
				street(r*n+c, (r+1)*n+c)
			}
		}
	}
	return g
}

// runWaits drives a whole engine end-to-end from a fixed seed and returns the
// per-completed-ride pickup waits, in completion order. Driver cancellation
// and repositioning are on so their paths are exercised too.
func runWaits(t *testing.T, seed int64) []float64 {
	t.Helper()
	g := streetGrid(10, 300)
	sc := &scenario.Scenario{
		Name:      "determinism",
		Duration:  120 * time.Second,
		Seed:      seed,
		StartTime: time.Unix(0, 0),
		Vehicles:  scenario.VehicleConfig{Count: 20, Spawn: "random"},
		Arrivals: scenario.ArrivalConfig{RateSegments: []scenario.RatePoint{
			{T: 0, Rate: 0.5}, {T: 120 * time.Second, Rate: 0.5},
		}},
	}

	engine := NewEngine(Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         10.0,
		SpeedMultiplier:  1.0,
	})
	d := engine.GetDispatcher()
	d.SetPolicy(dispatcher.NewBatchPolicy(3 * time.Second))
	d.SetDriverBehavior(dispatcher.DriverBehavior{CancelRate: 0.02}, seed)
	d.SetRepositioning(dispatcher.DefaultRepositioning(10 * time.Second))
	engine.SetStartTime(sc.StartTime)

	gen := scenario.NewGenerator(sc, g)
	engine.SetArrivalSource(gen)
	for _, n := range gen.VehicleSpawnNodes(g) {
		engine.SpawnVehicle(n)
	}

	const tickDt = time.Second / 10
	ticks := int((sc.Duration + sc.Duration/2) / tickDt)
	for i := 0; i < ticks; i++ {
		engine.Tick()
	}
	if d.RepositionCount() == 0 {
		t.Fatal("no car repositioned, so the run doesn't exercise it")
	}

	completed := d.GetCompletedRides()
	waits := make([]float64, 0, len(completed))
	for _, r := range completed {
		waits = append(waits, r.PickupTime.Sub(r.Request.RequestTime).Seconds())
	}
	return waits
}

// firstDiff returns the first index where a and b differ, or -1 if they match.
func firstDiff(a, b []float64) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

// Two full engine runs with the same seed must produce identical wait streams.
func TestEngineRunIsDeterministic(t *testing.T) {
	a := runWaits(t, 42)
	b := runWaits(t, 42)
	if len(a) == 0 {
		t.Fatal("no completed rides, nothing to compare")
	}
	if i := firstDiff(a, b); i >= 0 {
		t.Fatalf("same seed diverged at ride %d of %d/%d", i, len(a), len(b))
	}
}

// A different seed should change the outcome, or the test above proves nothing.
func TestEngineRunVariesWithSeed(t *testing.T) {
	if firstDiff(runWaits(t, 1), runWaits(t, 2)) < 0 {
		t.Error("different seeds produced identical results, so the seed isn't used")
	}
}
