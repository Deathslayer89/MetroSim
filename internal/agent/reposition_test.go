package agent

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

func TestRepositionStaysAvailableUntilArrival(t *testing.T) {
	g := loadGrid(t)
	v := NewVehicle(1, 0, g, pathfinding.NewPathPlanner(g, nil))
	if err := v.Reposition(2, nil); err != nil {
		t.Fatalf("reposition: %v", err)
	}
	if v.State != StateRepositioning || !v.IsAvailable() {
		t.Fatalf("after Reposition: want repositioning and available, got %s", v.State)
	}
	for i := 0; i < 100 && v.State == StateRepositioning; i++ {
		v.Move(1)
	}
	if v.State != StateIdle || v.CurrentNode != 2 {
		t.Errorf("want Idle at node 2, got %s at node %d", v.State, v.CurrentNode)
	}
}

// A new route planned mid-edge starts with the edge the car is on; otherwise
// finishing that edge would skip the new route's first edge.
func TestPlanRouteMidEdgeFinishesCurrentEdge(t *testing.T) {
	g := loadGrid(t)
	v := NewVehicle(1, 0, g, pathfinding.NewPathPlanner(g, nil))
	if err := v.Reposition(2, nil); err != nil {
		t.Fatalf("reposition: %v", err)
	}
	v.Move(2)
	if v.CurrentEdge == nil || v.CurrentEdge.ToNode != 1 {
		t.Fatalf("want the car partway along edge 0->1, got %+v", v.CurrentEdge)
	}

	v.SetDestination(4)
	if err := v.PlanRoute(); err != nil {
		t.Fatalf("plan route: %v", err)
	}
	if v.State != StateEnroute || v.Route[0] != v.CurrentEdge {
		t.Fatalf("want Enroute with the current edge first, got %s", v.State)
	}
	for i := 0; i < 100 && !v.HasReachedDestination(); i++ {
		v.Move(1)
	}
	if v.CurrentNode != 4 {
		t.Errorf("want arrival at node 4, stopped at node %d", v.CurrentNode)
	}
}
