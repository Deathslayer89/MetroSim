package graph

import "testing"

// 0->1->2->0 is a cycle; node 3 hangs off 2 with no way back, so it goes.
func TestKeepLargestSCCDropsUnreachable(t *testing.T) {
	g := NewGraph()
	for i := 0; i <= 3; i++ {
		g.AddNode(&Node{ID: i})
	}
	g.AddEdge(&Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 1, SpeedLimit: 1})
	g.AddEdge(&Edge{ID: 1, FromNode: 1, ToNode: 2, Length: 1, SpeedLimit: 1})
	g.AddEdge(&Edge{ID: 2, FromNode: 2, ToNode: 0, Length: 1, SpeedLimit: 1})
	g.AddEdge(&Edge{ID: 3, FromNode: 2, ToNode: 3, Length: 1, SpeedLimit: 1}) // tail, no way back

	removed := g.KeepLargestSCC()
	if removed != 1 {
		t.Fatalf("want 1 node removed, got %d", removed)
	}
	if g.NodeCount() != 3 {
		t.Errorf("want 3 nodes remaining, got %d", g.NodeCount())
	}
	if _, err := g.GetNode(3); err == nil {
		t.Error("node 3 should have been pruned")
	}
	// The edge into the pruned node must be gone too.
	if _, err := g.GetEdge(3); err == nil {
		t.Error("edge 3 (2->3) should have been pruned")
	}
	// Every surviving node still routes to every other.
	if len(g.GetNeighbors(2)) != 1 || g.GetNeighbors(2)[0].ToNode != 0 {
		t.Errorf("node 2 should only keep its 2->0 edge, got %v", g.GetNeighbors(2))
	}
}

// A graph that's already one SCC is left untouched.
func TestKeepLargestSCCNoOpWhenConnected(t *testing.T) {
	g := NewGraph()
	for i := 0; i <= 2; i++ {
		g.AddNode(&Node{ID: i})
	}
	g.AddEdge(&Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 1, SpeedLimit: 1})
	g.AddEdge(&Edge{ID: 1, FromNode: 1, ToNode: 2, Length: 1, SpeedLimit: 1})
	g.AddEdge(&Edge{ID: 2, FromNode: 2, ToNode: 0, Length: 1, SpeedLimit: 1})

	if removed := g.KeepLargestSCC(); removed != 0 {
		t.Errorf("connected graph should lose no nodes, removed %d", removed)
	}
}
