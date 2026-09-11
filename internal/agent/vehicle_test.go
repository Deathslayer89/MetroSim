package agent

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

func loadGrid(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load test grid: %v", err)
	}
	return g
}

// A vehicle given a destination follows the planned route and arrives Idle.
func TestVehicleMovement(t *testing.T) {
	g := loadGrid(t)
	vehicle := NewVehicle(1, 0, g, pathfinding.NewPathPlanner(g, nil))
	if !vehicle.IsIdle() {
		t.Fatalf("new vehicle: want Idle, got %s", vehicle.State)
	}

	vehicle.SetDestination(9)
	if err := vehicle.PlanRoute(); err != nil {
		t.Fatalf("plan route: %v", err)
	}
	if !vehicle.IsEnroute() || len(vehicle.Route) == 0 {
		t.Fatalf("after PlanRoute: want Enroute with a route, got %s with %d edges", vehicle.State, len(vehicle.Route))
	}

	for step := 0; step < 100 && !vehicle.HasReachedDestination(); step++ {
		vehicle.Move(1)
		if _, _, err := vehicle.GetPosition(); err != nil {
			t.Fatalf("position at step %d: %v", step, err)
		}
	}
	if !vehicle.HasReachedDestination() || vehicle.CurrentNode != 9 {
		t.Fatalf("want arrival at node 9 within 100 s, stopped at node %d", vehicle.CurrentNode)
	}
}

func TestVehicleStates(t *testing.T) {
	g := loadGrid(t)
	vehicle := NewVehicle(1, 0, g, pathfinding.NewPathPlanner(g, nil))

	// SetDestination only records the target; PlanRoute changes state.
	vehicle.SetDestination(5)
	if vehicle.State != StateIdle {
		t.Errorf("after SetDestination: want Idle until PlanRoute succeeds, got %s", vehicle.State)
	}
	if err := vehicle.PlanRoute(); err != nil {
		t.Fatalf("plan route: %v", err)
	}
	if vehicle.State != StateEnroute {
		t.Errorf("after PlanRoute: want Enroute, got %s", vehicle.State)
	}

	for i := 0; i < 100 && !vehicle.HasReachedDestination(); i++ {
		vehicle.Move(1)
	}
	if !vehicle.IsIdle() || vehicle.CurrentNode != 5 {
		t.Errorf("at the end: want Idle at node 5, got %s at node %d", vehicle.State, vehicle.CurrentNode)
	}
}
