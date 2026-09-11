package pathfinding

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// buildLinearGraph is the path 0-1-2-3 with unit-weight edges.
func buildLinearGraph() *graph.Graph {
	g := graph.NewGraph()
	for i := 0; i < 4; i++ {
		g.AddNode(&graph.Node{ID: i, Lat: 0, Lon: float64(i)})
	}
	for i := 0; i < 3; i++ {
		g.AddEdge(&graph.Edge{ID: i, FromNode: i, ToNode: i + 1, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 1})
	}
	return g
}

func TestFindPathDirect(t *testing.T) {
	g := buildLinearGraph()
	p := NewPathPlanner(g, graph.EuclideanDistance)
	path, err := p.FindPath(0, 1)
	if err != nil {
		t.Fatalf("FindPath(0,1): %v", err)
	}
	if len(path) != 1 || path[0].FromNode != 0 || path[0].ToNode != 1 {
		t.Errorf("want one edge 0->1, got %+v", path)
	}
}

func TestFindPathMultiHop(t *testing.T) {
	g := buildLinearGraph()
	p := NewPathPlanner(g, graph.EuclideanDistance)
	path, err := p.FindPath(0, 3)
	if err != nil {
		t.Fatalf("FindPath(0,3): %v", err)
	}
	if len(path) != 3 {
		t.Fatalf("want 3 edges, got %d", len(path))
	}
	for i, e := range path {
		if e.FromNode != i || e.ToNode != i+1 {
			t.Errorf("step %d: want %d->%d, got %d->%d", i, i, i+1, e.FromNode, e.ToNode)
		}
	}
}

func TestFindPathStartEqualsGoal(t *testing.T) {
	g := buildLinearGraph()
	p := NewPathPlanner(g, graph.EuclideanDistance)
	path, err := p.FindPath(2, 2)
	if err != nil {
		t.Fatalf("start==goal should succeed, got %v", err)
	}
	if len(path) != 0 {
		t.Errorf("start==goal should be zero-edge path, got %d edges", len(path))
	}
}

func TestFindPathDisconnected(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 0, Lon: 0})
	g.AddNode(&graph.Node{ID: 1, Lat: 0, Lon: 1})
	// No edges.
	p := NewPathPlanner(g, graph.EuclideanDistance)
	if _, err := p.FindPath(0, 1); err == nil {
		t.Error("disconnected: want error, got nil")
	}
}

func TestFindPathMissingNode(t *testing.T) {
	g := buildLinearGraph()
	p := NewPathPlanner(g, graph.EuclideanDistance)
	if _, err := p.FindPath(0, 99); err == nil {
		t.Error("missing goal node: want error, got nil")
	}
	if _, err := p.FindPath(99, 0); err == nil {
		t.Error("missing start node: want error, got nil")
	}
}

// FindPathWithWeights must prefer the cheaper detour when base weights would
// have chosen the direct edge. Coords are all (0,0) so the Euclidean heuristic
// degenerates to 0 (plain Dijkstra), so the answer is optimal regardless of
// heuristic admissibility.
func TestFindPathWithWeightsBeatsBase(t *testing.T) {
	g := graph.NewGraph()
	for i := 0; i < 4; i++ {
		g.AddNode(&graph.Node{ID: i, Lat: 0, Lon: 0})
	}
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 3, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 1})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 0, ToNode: 1, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 2})
	g.AddEdge(&graph.Edge{ID: 2, FromNode: 1, ToNode: 2, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 2})
	g.AddEdge(&graph.Edge{ID: 3, FromNode: 2, ToNode: 3, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 2})

	p := NewPathPlanner(g, graph.EuclideanDistance)

	base, err := p.FindPath(0, 3)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	if len(base) != 1 || base[0].ID != 0 {
		t.Errorf("base: want single direct edge, got %+v", base)
	}

	// With the direct edge at 100, the detour 0-1-2-3 at 6 wins.
	weights := map[int]float64{0: 100}
	custom, err := p.FindPathWithWeights(0, 3, weights)
	if err != nil {
		t.Fatalf("weighted: %v", err)
	}
	if len(custom) != 3 {
		t.Errorf("weighted: want 3-edge detour, got %d edges", len(custom))
	}
}
