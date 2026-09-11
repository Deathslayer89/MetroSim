package agent

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

// When an edge ahead spikes, MaybeReplan should switch to a cheaper route that
// avoids it.
func TestMaybeReplanAvoidsSpikedEdge(t *testing.T) {
	g := graph.NewGraph()
	for i := 0; i <= 3; i++ {
		g.AddNode(&graph.Node{ID: i}) // all at (0,0): heuristic 0, so A* is Dijkstra
	}
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 10, SpeedLimit: 10, BaseWeight: 1})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 1, ToNode: 3, Length: 10, SpeedLimit: 10, BaseWeight: 1})
	g.AddEdge(&graph.Edge{ID: 2, FromNode: 0, ToNode: 2, Length: 10, SpeedLimit: 10, BaseWeight: 3})
	g.AddEdge(&graph.Edge{ID: 3, FromNode: 2, ToNode: 3, Length: 10, SpeedLimit: 10, BaseWeight: 1})

	v := NewVehicle(1, 0, g, pathfinding.NewPathPlanner(g, graph.EuclideanDistance))
	v.SetDestination(3)
	if err := v.PlanRoute(); err != nil {
		t.Fatal(err)
	}
	if len(v.Route) != 2 || v.Route[1].ID != 1 {
		t.Fatalf("want initial route via edge 1 (0->1->3), got %v", routeIDs(v.Route))
	}

	spike := map[int]float64{1: 100}
	if !v.MaybeReplan(spike, spike) {
		t.Fatal("want replan after edge 1 spiked")
	}
	for _, e := range v.Route {
		if e.ID == 1 {
			t.Errorf("replanned route still uses spiked edge 1: %v", routeIDs(v.Route))
		}
	}
}

func routeIDs(route []*graph.Edge) []int {
	ids := make([]int, len(route))
	for i, e := range route {
		ids[i] = e.ID
	}
	return ids
}

// chain builds a straight path of len(lengths) edges (node i -> i+1) and returns
// a vehicle parked at node 0, routed to the last node, ready to Move.
func chain(speeds, lengths []float64) *Vehicle {
	g := graph.NewGraph()
	for i := 0; i <= len(lengths); i++ {
		g.AddNode(&graph.Node{ID: i, Lat: 0, Lon: float64(i) * 0.001})
	}
	route := make([]*graph.Edge, len(lengths))
	for i := range lengths {
		e := &graph.Edge{ID: i, FromNode: i, ToNode: i + 1, Length: lengths[i], SpeedLimit: speeds[i]}
		g.AddEdge(e)
		route[i] = e
	}
	v := NewVehicle(1, 0, g, nil)
	v.Destination = len(lengths)
	v.Route = route
	v.RouteIndex = 0
	v.State = StateEnroute
	return v
}

// One tick covering two whole edges must land exactly on the third node with
// progress reset: leftover time carries across edges instead of being lost.
func TestMoveCarriesAcrossMultipleEdges(t *testing.T) {
	v := chain([]float64{10, 10, 10}, []float64{10, 10, 10})
	v.Move(2.0) // 20 m == edge0 + edge1
	if v.CurrentNode != 2 {
		t.Errorf("want CurrentNode 2 after crossing two edges, got %d", v.CurrentNode)
	}
	if v.RouteIndex != 2 {
		t.Errorf("want RouteIndex 2, got %d", v.RouteIndex)
	}
	if v.Progress != 0 {
		t.Errorf("want Progress 0 (landed on node), got %v", v.Progress)
	}
}

// Landing exactly on a node crosses it cleanly (no double-count, no skip).
func TestMoveExactNodeLanding(t *testing.T) {
	v := chain([]float64{10, 10}, []float64{10, 10})
	v.Move(1.0) // exactly edge0's length
	if v.CurrentNode != 1 || v.Progress != 0 {
		t.Errorf("want node 1 progress 0, got node %d progress %v", v.CurrentNode, v.Progress)
	}
	if v.RouteIndex != 1 {
		t.Errorf("want RouteIndex 1, got %d", v.RouteIndex)
	}
}

// A zero-length edge in the route must be traversed without dividing by zero or
// looping forever.
func TestMoveZeroLengthEdge(t *testing.T) {
	v := chain([]float64{10, 10, 10}, []float64{10, 0, 10})
	v.Move(0.5) // 5 m onto edge0
	v.Move(1.0) // 10 m: finishes edge0, crosses the zero-length edge1, onto edge2
	if v.CurrentNode != 2 {
		t.Errorf("want CurrentNode 2 (past the zero-length edge), got %d", v.CurrentNode)
	}
	if v.State != StateEnroute {
		t.Errorf("want still Enroute, got %s", v.State)
	}
}

// Reaching the destination mid-tick goes Idle and stops (no overshoot).
func TestMoveReachesDestination(t *testing.T) {
	v := chain([]float64{10}, []float64{10})
	v.Move(5.0) // far more than the 10 m route
	if !v.HasReachedDestination() {
		t.Errorf("want reached destination, got state %s node %d", v.State, v.CurrentNode)
	}
	if v.CurrentNode != 1 {
		t.Errorf("want CurrentNode 1, got %d", v.CurrentNode)
	}
}
