package dispatcher

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

// Greedy must fall through to a reachable driver when the nearest one has no
// route to the pickup, instead of stranding the request.
func TestGreedyFallsBackPastUnreachableNearest(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.7700, Lon: -122.4200}) // driver A: nearest, unreachable
	g.AddNode(&graph.Node{ID: 1, Lat: 37.7710, Lon: -122.4200}) // driver B: farther, reachable
	g.AddNode(&graph.Node{ID: 2, Lat: 37.7700, Lon: -122.4200}) // pickup (co-located with A)
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 1, ToNode: 2, Length: 1, SpeedLimit: 1, BaseWeight: 5})
	// No edge leaves node 0, so driver A can't reach the pickup.

	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)
	drvA := agent.NewVehicle(1, 0, g, planner)
	drvB := agent.NewVehicle(2, 1, g, planner)
	vehicles := map[int]*agent.Vehicle{1: drvA, 2: drvB}

	idx := NewH3DriverIndex()
	for _, v := range vehicles {
		lat, lon, _ := v.GetPosition()
		idx.Insert(v.ID, lat, lon)
	}

	out := GreedyPolicy{}.Match(MatchCtx{
		Pending:     []*Request{{ID: 1, PickupNode: 2}},
		DriverIndex: idx,
		Vehicles:    vehicles,
		Graph:       g,
		PathPlanner: planner,
	})
	if len(out) != 1 {
		t.Fatalf("want 1 assignment (fallback to reachable driver), got %d", len(out))
	}
	if out[0].DriverID != 2 {
		t.Errorf("want reachable driver 2, got %d", out[0].DriverID)
	}
}

// NearestK must be deterministic when candidates tie on distance: identical
// coords resolve by driver ID, independent of insertion/map-iteration order.
func TestNearestKDeterministicOnTies(t *testing.T) {
	idx := NewH3DriverIndex()
	for _, id := range []int{5, 3, 9, 1, 7} {
		idx.Insert(id, 37.77, -122.42) // one spot, so every distance ties
	}
	want := []int{1, 3, 5, 7, 9}
	for trial := 0; trial < 5; trial++ {
		got := idx.NearestK(37.77, -122.42, 5)
		if len(got) != len(want) {
			t.Fatalf("want %d candidates, got %d", len(want), len(got))
		}
		for i, w := range want {
			if got[i].ID != w {
				t.Fatalf("trial %d: order not deterministic; want %v, got id %d at %d", trial, want, got[i].ID, i)
			}
		}
	}
}
