package traffic

import (
	"math"
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// buildSingleEdgeGraph has one edge with known length, lanes and speed, so its
// capacity and base time are known too.
func buildSingleEdgeGraph() *graph.Graph {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 0, Lon: 0})
	g.AddNode(&graph.Node{ID: 1, Lat: 0, Lon: 1})
	// 140 m, 2 lanes, 10 m/s: BaseWeight 14 s, capacity 2*140/7 = 40
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 140, Lanes: 2, SpeedLimit: 10})
	return g
}

func TestCongestionFactorAtZeroDensity(t *testing.T) {
	g := buildSingleEdgeGraph()
	tm := NewTrafficModel(g, CongestionParams{Alpha: 0.15, Beta: 4})
	if got := tm.GetCongestionFactor(0); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("zero density: want factor 1.0, got %v", got)
	}
}

func TestCongestionFactorAtCapacity(t *testing.T) {
	// At density == capacity, ratio=1, factor = 1 + alpha * 1^beta = 1 + alpha.
	g := buildSingleEdgeGraph()
	tm := NewTrafficModel(g, CongestionParams{Alpha: 0.5, Beta: 2})

	cap := int(tm.GetEdgeCapacity(0))
	tm.edgeDensities[0] = cap

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

	cap := tm.GetEdgeCapacity(0)
	density := int(cap * 0.8)
	tm.edgeDensities[0] = density

	ratio := float64(density) / cap
	want := 1.0 + alpha*math.Pow(ratio, beta)
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

	cap := int(tm.GetEdgeCapacity(0))
	tm.edgeDensities[0] = cap

	tm.ComputeEdgeWeights()
	baseEdge, _ := g.GetEdge(0)
	want := baseEdge.BaseWeight * 2.0 // 1 + 1*1 = 2 at capacity
	got := tm.GetEdgeWeight(0)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("weight at capacity (alpha=1,beta=2): want %v, got %v", want, got)
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
