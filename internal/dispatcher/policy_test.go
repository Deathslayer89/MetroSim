package dispatcher

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

// buildAsymmetricMatchGraph has two drivers and two pickups. Routed ETAs: A to
// pickup 1 is 5, A to 2 is 1, B to 1 is 6, B to 2 is 10. Greedy gives request 1
// its nearest driver, A, and pays 5 + 10 = 15; batch pays 1 + 6 = 7.
func buildAsymmetricMatchGraph() *graph.Graph {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.770, Lon: -122.420}) // driver A
	g.AddNode(&graph.Node{ID: 1, Lat: 37.771, Lon: -122.420}) // driver B
	g.AddNode(&graph.Node{ID: 2, Lat: 37.770, Lon: -122.420}) // req1 pickup
	g.AddNode(&graph.Node{ID: 3, Lat: 37.771, Lon: -122.420}) // req2 pickup
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 2, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 5})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 0, ToNode: 3, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 1})
	g.AddEdge(&graph.Edge{ID: 2, FromNode: 1, ToNode: 2, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 6})
	g.AddEdge(&graph.Edge{ID: 3, FromNode: 1, ToNode: 3, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 10})
	return g
}

func totalAssignmentCost(a []Assignment) float64 {
	var s float64
	for _, x := range a {
		s += x.Cost
	}
	return s
}

func TestBatchBeatsGreedyOnConstrainedSupply(t *testing.T) {
	g := buildAsymmetricMatchGraph()
	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)

	drvA := agent.NewVehicle(1, 0, g, planner)
	drvB := agent.NewVehicle(2, 1, g, planner)
	vehicles := map[int]*agent.Vehicle{1: drvA, 2: drvB}

	req1 := &Request{ID: 1, PickupNode: 2}
	req2 := &Request{ID: 2, PickupNode: 3}

	freshIndex := func() *H3DriverIndex {
		idx := NewH3DriverIndex()
		for _, v := range vehicles {
			lat, lon, _ := v.GetPosition()
			idx.Insert(v.ID, lat, lon)
		}
		return idx
	}

	mkCtx := func(idx *H3DriverIndex) MatchCtx {
		return MatchCtx{
			Pending:     []*Request{req1, req2},
			DriverIndex: idx,
			Vehicles:    vehicles,
			EdgeWeights: nil,
			Graph:       g,
			PathPlanner: planner,
			Now:         time.Unix(1_700_000_000, 0),
		}
	}

	greedyAssign := GreedyPolicy{}.Match(mkCtx(freshIndex()))
	batchAssign := NewBatchPolicy(0).Match(mkCtx(freshIndex()))

	if len(greedyAssign) != 2 || len(batchAssign) != 2 {
		t.Fatalf("both policies should match both requests; greedy=%d batch=%d",
			len(greedyAssign), len(batchAssign))
	}

	if g, b := totalAssignmentCost(greedyAssign), totalAssignmentCost(batchAssign); g != 15 || b != 7 {
		t.Fatalf("want greedy 15 and batch 7, got greedy=%v batch=%v", g, b)
	}
}

// Each pickup has its own driver one edge away, so there is nothing to trade
// off: both policies match all three at total cost 3.
func TestBatchEqualsGreedyOnPlentifulSupply(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.770, Lon: -122.420})
	g.AddNode(&graph.Node{ID: 1, Lat: 37.771, Lon: -122.420})
	g.AddNode(&graph.Node{ID: 2, Lat: 37.772, Lon: -122.420})
	g.AddNode(&graph.Node{ID: 3, Lat: 37.770, Lon: -122.420})
	g.AddNode(&graph.Node{ID: 4, Lat: 37.771, Lon: -122.420})
	g.AddNode(&graph.Node{ID: 5, Lat: 37.772, Lon: -122.420})
	g.AddEdge(&graph.Edge{ID: 0, FromNode: 0, ToNode: 3, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 1})
	g.AddEdge(&graph.Edge{ID: 1, FromNode: 1, ToNode: 4, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 1})
	g.AddEdge(&graph.Edge{ID: 2, FromNode: 2, ToNode: 5, Length: 1, Lanes: 1, SpeedLimit: 1, BaseWeight: 1})

	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)

	vehicles := map[int]*agent.Vehicle{
		1: agent.NewVehicle(1, 0, g, planner),
		2: agent.NewVehicle(2, 1, g, planner),
		3: agent.NewVehicle(3, 2, g, planner),
	}

	mkCtx := func() MatchCtx {
		idx := NewH3DriverIndex()
		for _, v := range vehicles {
			lat, lon, _ := v.GetPosition()
			idx.Insert(v.ID, lat, lon)
		}
		return MatchCtx{
			Pending: []*Request{
				{ID: 1, PickupNode: 3},
				{ID: 2, PickupNode: 4},
				{ID: 3, PickupNode: 5},
			},
			DriverIndex: idx,
			Vehicles:    vehicles,
			EdgeWeights: nil,
			Graph:       g,
			PathPlanner: planner,
			Now:         time.Unix(1_700_000_000, 0),
		}
	}

	greedyCost := totalAssignmentCost(GreedyPolicy{}.Match(mkCtx()))
	batchCost := totalAssignmentCost(NewBatchPolicy(0).Match(mkCtx()))
	if greedyCost != 3 || batchCost != 3 {
		t.Errorf("want total cost 3 from both, got greedy=%v batch=%v", greedyCost, batchCost)
	}
}

// Batch must hold until its window elapses; mid-window calls should return nil.
func TestBatchPolicyHoldsForWindow(t *testing.T) {
	g := buildAsymmetricMatchGraph()
	planner := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)
	vehicles := map[int]*agent.Vehicle{
		1: agent.NewVehicle(1, 0, g, planner),
		2: agent.NewVehicle(2, 1, g, planner),
	}
	idx := NewH3DriverIndex()
	for _, v := range vehicles {
		lat, lon, _ := v.GetPosition()
		idx.Insert(v.ID, lat, lon)
	}
	ctx := MatchCtx{
		Pending:     []*Request{{ID: 1, PickupNode: 2}},
		DriverIndex: idx,
		Vehicles:    vehicles,
		Graph:       g,
		PathPlanner: planner,
		Now:         time.Unix(1_700_000_000, 0),
	}

	b := NewBatchPolicy(3 * time.Second)
	first := b.Match(ctx)
	if len(first) != 1 {
		t.Fatalf("first call should fire; got %d assignments", len(first))
	}

	ctx.Now = ctx.Now.Add(1 * time.Second)
	mid := b.Match(ctx)
	if len(mid) != 0 {
		t.Fatalf("mid-window call should return nil; got %d", len(mid))
	}

	ctx.Now = ctx.Now.Add(3 * time.Second)
	late := b.Match(ctx)
	if len(late) != 1 {
		t.Fatalf("post-window call should fire; got %d", len(late))
	}
}
