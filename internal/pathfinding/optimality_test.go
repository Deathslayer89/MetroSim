package pathfinding

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// detourGraph has a slow straight route S-M-G (1800 s) and a fast detour S-D-G
// (500 s) that starts by heading away from the goal.
func detourGraph() *graph.Graph {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.00, Lon: -122.00}) // S
	g.AddNode(&graph.Node{ID: 1, Lat: 37.00, Lon: -122.05}) // M, on the straight line
	g.AddNode(&graph.Node{ID: 2, Lat: 37.00, Lon: -122.10}) // G
	g.AddNode(&graph.Node{ID: 3, Lat: 37.05, Lon: -122.05}) // D, north of the line
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 4432, SpeedLimit: 5, BaseWeight: 900})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 1, ToNode: 2, Length: 4432, SpeedLimit: 5, BaseWeight: 900})
	g.AddEdge(&graph.Edge{ID: 2, FromNode: 0, ToNode: 3, Length: 7100, SpeedLimit: 30, BaseWeight: 250})
	g.AddEdge(&graph.Edge{ID: 3, FromNode: 3, ToNode: 2, Length: 7100, SpeedLimit: 30, BaseWeight: 250})
	return g
}

func detourCost(t *testing.T, p *PathPlanner) float64 {
	t.Helper()
	route, err := p.FindPath(0, 2)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	var c float64
	for _, e := range route {
		c += e.BaseWeight
	}
	return c
}

func TestAdmissibleHeuristicFindsDetour(t *testing.T) {
	g := detourGraph()
	if c := detourCost(t, NewPathPlanner(g, AdmissibleTimeHeuristic(g))); c != 500 {
		t.Errorf("admissible: want the 500 s detour, got %.0f s", c)
	}
}

// Meters against second-valued costs behaves like weighted A* with a weight of
// about the top speed, so the search sticks to the straight line.
func TestMetersHeuristicTakesStraightRoute(t *testing.T) {
	g := detourGraph()
	if c := detourCost(t, NewPathPlanner(g, graph.EuclideanDistance)); c != 1800 {
		t.Errorf("meters heuristic: want the 1800 s straight route, got %.0f s", c)
	}
}

func TestDefaultHeuristicStaysWithinItsWeight(t *testing.T) {
	g := detourGraph()
	if c := detourCost(t, NewPathPlanner(g, nil)); c > DefaultHeuristicWeight*500 {
		t.Errorf("default: route %.0f s exceeds %.0fx the 500 s optimum", c, DefaultHeuristicWeight)
	}
}
