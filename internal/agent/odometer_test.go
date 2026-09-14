package agent

import (
	"math"
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

// Driving to reposition counts on both odometers; any other driving, with a
// rider or on the way to one, counts on the total alone.
func TestOdometersSplitRepositioningFromOtherDriving(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.770, Lon: -122.42})
	g.AddNode(&graph.Node{ID: 1, Lat: 37.771, Lon: -122.42})
	g.AddNode(&graph.Node{ID: 2, Lat: 37.772, Lon: -122.42})
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 100, SpeedLimit: 10, Lanes: 1})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 1, ToNode: 2, Length: 150, SpeedLimit: 10, Lanes: 1})
	v := NewVehicle(1, 0, g, pathfinding.NewPathPlanner(g, nil))

	if err := v.Reposition(1, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30 && v.CurrentNode != 1; i++ {
		v.Move(0.7)
	}
	v.SetDestination(2)
	if err := v.PlanRoute(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30 && v.CurrentNode != 2; i++ {
		v.Move(0.7)
	}
	if math.Abs(v.RepositionMeters-100) > 1e-9 || math.Abs(v.DrivenMeters-250) > 1e-9 {
		t.Errorf("want 100 m repositioning of 250 m driven, got %.3f of %.3f", v.RepositionMeters, v.DrivenMeters)
	}
}
