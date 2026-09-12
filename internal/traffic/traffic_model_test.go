package traffic

import (
	"math"
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// buildSingleEdgeGraph has one edge with known length, lanes and speed, so its
// capacity and base time are known too. On its own, the edge is a whole link.
func buildSingleEdgeGraph() *graph.Graph {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 0, Lon: 0})
	g.AddNode(&graph.Node{ID: 1, Lat: 0, Lon: 1})
	// 140 m, 2 lanes, 10 m/s: BaseWeight 14 s, capacity 2*140/7 = 40
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 140, Lanes: 2, SpeedLimit: 10})
	return g
}

// street is a one-lane road of n segments, each segLen meters, from node 0 to
// node n. Edge 2i runs from node i to i+1, and on a two-way street edge 2i+1
// runs back.
func street(n int, segLen float64, twoWay bool) *graph.Graph {
	g := graph.NewGraph()
	for i := 0; i <= n; i++ {
		g.AddNode(&graph.Node{ID: i, Lat: 37.77, Lon: -122.42 + float64(i)*segLen/88_000})
	}
	for i := 0; i < n; i++ {
		g.AddEdge(&graph.Edge{ID: 2 * i, FromNode: i, ToNode: i + 1, Length: segLen, Lanes: 1, SpeedLimit: 10})
		if twoWay {
			g.AddEdge(&graph.Edge{ID: 2*i + 1, FromNode: i + 1, ToNode: i, Length: segLen, Lanes: 1, SpeedLimit: 10})
		}
	}
	return g
}

// carsOn returns n cars partway along e.
func carsOn(e *graph.Edge, n int) []*agent.Vehicle {
	cars := make([]*agent.Vehicle, n)
	for i := range cars {
		cars[i] = &agent.Vehicle{ID: i + 1, CurrentEdge: e}
	}
	return cars
}

func TestCongestionFactorAtZeroDensity(t *testing.T) {
	g := buildSingleEdgeGraph()
	tm := NewTrafficModel(g, CongestionParams{Alpha: 0.15, Beta: 4})
	if got := tm.GetCongestionFactor(0); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("zero density: want factor 1.0, got %v", got)
	}
}

func TestCongestionFactorAtCapacity(t *testing.T) {
	// With as many other cars as the edge holds, ratio=1 and factor = 1 + alpha.
	g := buildSingleEdgeGraph()
	tm := NewTrafficModel(g, CongestionParams{Alpha: 0.5, Beta: 2})

	others := int(tm.GetEdgeCapacity(0))
	tm.UpdateDensities(carsOn(g.Edges[0], others+1))

	got := tm.GetCongestionFactor(0)
	want := 1.0 + 0.5
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("density==capacity: want %v, got %v", want, got)
	}
}

func TestCongestionFactorBPRFormula(t *testing.T) {
	// Check 1 + alpha*(d/c)^beta where the answer isn't trivial.
	g := buildSingleEdgeGraph()
	alpha, beta := 0.15, 4.0
	tm := NewTrafficModel(g, CongestionParams{Alpha: alpha, Beta: beta})

	capacity := tm.GetEdgeCapacity(0)
	others := int(capacity * 0.8)
	tm.UpdateDensities(carsOn(g.Edges[0], others+1))

	want := 1.0 + alpha*math.Pow(float64(others)/capacity, beta)
	got := tm.GetCongestionFactor(0)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("BPR at d/c=0.8: want %v, got %v", want, got)
	}
}

func TestEdgeCapacityFormula(t *testing.T) {
	// capacity = lanes * length / 7 (5m vehicle + 2m spacing)
	g := buildSingleEdgeGraph()
	tm := NewTrafficModel(g, DefaultCongestionParams())
	got := tm.GetEdgeCapacity(0)
	want := 2.0 * 140.0 / 7.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("capacity: want %v (= 2 lanes * 140m / 7), got %v", want, got)
	}
}

func TestComputeEdgeWeightsScalesByCongestion(t *testing.T) {
	g := buildSingleEdgeGraph()
	tm := NewTrafficModel(g, CongestionParams{Alpha: 1.0, Beta: 2.0})

	others := int(tm.GetEdgeCapacity(0))
	tm.UpdateDensities(carsOn(g.Edges[0], others+1))

	tm.ComputeEdgeWeights()
	baseEdge, _ := g.GetEdge(0)
	want := baseEdge.BaseWeight * 2.0 // 1 + 1*1 = 2 at capacity
	got := tm.GetEdgeWeight(0)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("weight at capacity (alpha=1,beta=2): want %v, got %v", want, got)
	}
}

// A car alone isn't slowed, however short its segment. Counted per segment, one
// car on 4 m of road was over the segment's capacity and crawled at 4x.
func TestLoneCarIsNotSlowedByItself(t *testing.T) {
	g := street(10, 4, true)
	tm := NewTrafficModel(g, DemoCongestionParams())
	tm.UpdateDensities(carsOn(g.Edges[4], 1))
	tm.ComputeEdgeWeights()
	if w, free := tm.GetEdgeWeight(4), g.Edges[4].BaseWeight; w != free {
		t.Errorf("a car alone takes %.2f s on its edge, free flow is %.2f s", w, free)
	}
}

// Cars on different segments of one stretch of road slow each other, and the
// empty segments between them, just as if they shared a segment.
func TestCarsOnOneLinkSlowTheWholeLink(t *testing.T) {
	g := street(5, 7, true) // each direction holds five cars
	tm := NewTrafficModel(g, DemoCongestionParams())
	tm.UpdateDensities(append(carsOn(g.Edges[0], 1), carsOn(g.Edges[8], 2)...))
	tm.ComputeEdgeWeights()

	want := 1 + math.Pow(2.0/5, 2) // two other cars where five fit
	for id := 0; id < 10; id += 2 {
		if got := tm.GetEdgeWeight(id) / g.Edges[id].BaseWeight; math.Abs(got-want) > 1e-9 {
			t.Errorf("edge %d runs at %.3fx free flow, want %.3fx", id, got, want)
		}
	}
	for id := 1; id < 10; id += 2 {
		if got := tm.GetEdgeWeight(id); got != g.Edges[id].BaseWeight {
			t.Errorf("edge %d, in the other direction, slowed to %.2f s", id, got)
		}
	}
}

// A link ends where another road branches off or merges in, so cars before the
// junction don't slow the road after it. The road is one-way, so only the side
// road marks the junction.
func TestLinksEndAtJunctions(t *testing.T) {
	for _, side := range []struct {
		name     string
		from, to int
	}{{"branching off", 2, 100}, {"merging in", 100, 2}} {
		t.Run(side.name, func(t *testing.T) {
			g := street(4, 7, false)
			g.AddNode(&graph.Node{ID: 100, Lat: 37.771, Lon: -122.4198})
			g.AddEdge(&graph.Edge{ID: 100, FromNode: side.from, ToNode: side.to, Length: 7, Lanes: 1, SpeedLimit: 10})
			tm := NewTrafficModel(g, DemoCongestionParams())
			tm.UpdateDensities(carsOn(g.Edges[0], 3)) // on 0 -> 1; with 1 -> 2 it holds two cars
			tm.ComputeEdgeWeights()

			if got, want := tm.GetEdgeWeight(2), 2*g.Edges[2].BaseWeight; math.Abs(got-want) > 1e-9 {
				t.Errorf("edge 1 -> 2 takes %.2f s, want %.2f s: two other cars where two fit", got, want)
			}
			if got := tm.GetEdgeWeight(4); got != g.Edges[4].BaseWeight {
				t.Errorf("edge 2 -> 3, past the junction, slowed to %.2f s", got)
			}
		})
	}
}

// A jam that grows by one car a tick changes less than 10% each tick. It must
// still be reported every time it grows 10% past the last report.
func TestGradualJamIsReported(t *testing.T) {
	g := buildSingleEdgeGraph()
	e := g.Edges[0]
	tm := NewTrafficModel(g, DemoCongestionParams())
	var cars []*agent.Vehicle
	last := e.BaseWeight
	reports := 0
	for n := 1; n <= 80; n++ {
		cars = append(cars, &agent.Vehicle{ID: n, CurrentEdge: e})
		tm.UpdateDensities(cars)
		if w, ok := tm.ComputeEdgeWeights()[0]; ok {
			reports++
			last = w
		}
		if w := tm.GetEdgeWeight(0); w > last*1.1+1e-9 {
			t.Fatalf("with %d cars the edge takes %.1f s, over 10%% more than the %.1f s last reported", n, w, last)
		}
	}
	if reports == 0 {
		t.Fatal("the edge jammed without ever being reported")
	}
}
