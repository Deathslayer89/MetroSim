package dispatcher

import (
	"testing"
	"time"

	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

// clusteredGraph builds a fully connected mesh of perCluster nodes around each
// center. Centers sit in different H3 r5 cells and no edge joins two clusters.
func clusteredGraph(centers [][2]float64, perCluster int) (*graph.Graph, [][]int) {
	g := graph.NewGraph()
	nodeID, edgeID := 0, 0
	clusters := make([][]int, len(centers))
	for ci, c := range centers {
		base := nodeID
		ids := make([]int, 0, perCluster)
		for i := 0; i < perCluster; i++ {
			g.AddNode(&graph.Node{ID: nodeID, Lat: c[0] + float64(i)*0.0005, Lon: c[1] + float64(i)*0.0005})
			ids = append(ids, nodeID)
			nodeID++
		}
		for i := 0; i < perCluster; i++ {
			for j := 0; j < perCluster; j++ {
				if i == j {
					continue
				}
				g.AddEdge(&graph.Edge{ID: edgeID, FromNode: base + i, ToNode: base + j, Length: 100, SpeedLimit: 10, Lanes: 1})
				edgeID++
			}
		}
		clusters[ci] = ids
	}
	return g, clusters
}

// buildCtx puts an idle driver and a pending request on every node; each
// request's destination is the next node in its cluster.
func buildCtx(g *graph.Graph, clusters [][]int) MatchCtx {
	planner := pathfinding.NewPathPlanner(g, nil)
	idx := NewH3DriverIndex()
	vehicles := make(map[int]*agent.Vehicle)
	var pending []*Request
	reqID := 1
	for _, ids := range clusters {
		for i, nodeID := range ids {
			n, _ := g.GetNode(nodeID)
			v := agent.NewVehicle(nodeID+1, nodeID, g, planner)
			vehicles[v.ID] = v
			idx.Insert(v.ID, n.Lat, n.Lon)
			pending = append(pending, &Request{
				ID:              reqID,
				PickupNode:      nodeID,
				DestinationNode: ids[(i+1)%len(ids)],
				RequestTime:     time.Unix(0, 0),
			})
			reqID++
		}
	}
	return MatchCtx{
		Pending:     pending,
		DriverIndex: idx,
		Vehicles:    vehicles,
		Graph:       g,
		PathPlanner: planner,
		Now:         time.Unix(100, 0),
	}
}

// straddleR5 walks east from (lat, lon) and returns two points about 90 m apart
// that fall in different H3 r5 cells.
func straddleR5(t *testing.T, lat, lon float64) (west, east h3.LatLng) {
	t.Helper()
	cellAt := func(p h3.LatLng) h3.Cell {
		c, err := h3.LatLngToCell(p, 5)
		if err != nil {
			t.Fatalf("LatLngToCell: %v", err)
		}
		return c
	}
	prev := h3.LatLng{Lat: lat, Lng: lon}
	for i := 0; i < 1000; i++ {
		next := h3.LatLng{Lat: lat, Lng: prev.Lng + 0.001}
		if cellAt(next) != cellAt(prev) {
			return prev, next
		}
		prev = next
	}
	t.Fatal("no r5 boundary within 1000 steps")
	return
}

// One driver sits between two pickups in adjacent r5 regions, so both regions
// see it as a candidate. The first region to solve takes it; the second must
// come up empty instead of reusing it.
func TestRegionShardedBoundaryDriverAssignedOnce(t *testing.T) {
	west, east := straddleR5(t, 37.77, -122.42)
	midLon := (west.Lng + east.Lng) / 2
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: west.Lat, Lon: west.Lng})
	g.AddNode(&graph.Node{ID: 1, Lat: east.Lat, Lon: east.Lng})
	g.AddNode(&graph.Node{ID: 2, Lat: west.Lat, Lon: midLon})
	for i, e := range [][2]int{{2, 0}, {2, 1}, {0, 1}, {1, 0}} {
		g.AddEdge(&graph.Edge{ID: i, FromNode: e[0], ToNode: e[1], Length: 50, SpeedLimit: 10, Lanes: 1})
	}
	planner := pathfinding.NewPathPlanner(g, nil)
	driver := agent.NewVehicle(1, 2, g, planner)
	idx := NewH3DriverIndex()
	idx.Insert(driver.ID, west.Lat, midLon)
	ctx := MatchCtx{
		Pending: []*Request{
			{ID: 1, PickupNode: 0, DestinationNode: 1, RequestTime: time.Unix(0, 0)},
			{ID: 2, PickupNode: 1, DestinationNode: 0, RequestTime: time.Unix(0, 0)},
		},
		DriverIndex: idx,
		Vehicles:    map[int]*agent.Vehicle{driver.ID: driver},
		Graph:       g,
		PathPlanner: planner,
		Now:         time.Unix(100, 0),
	}

	if got := NewRegionShardedBatchPolicy(0).Match(ctx); len(got) != 1 {
		t.Fatalf("one driver shared by two regions: want 1 assignment, got %d", len(got))
	}
}

// A driver should only ever be matched to a request in its own cluster, because
// cross-cluster routing is unreachable (infinite ETA).
func TestRegionShardedKeepsAssignmentsInRegion(t *testing.T) {
	centers := [][2]float64{{37.77, -122.42}, {40.71, -74.0}}
	g, clusters := clusteredGraph(centers, 5)
	ctx := buildCtx(g, clusters)

	clusterOf := map[int]int{}
	for ci, ids := range clusters {
		for _, id := range ids {
			clusterOf[id] = ci
		}
	}

	got := NewRegionShardedBatchPolicy(0).Match(ctx)
	if len(got) != 10 {
		t.Fatalf("want all 10 requests matched, got %d", len(got))
	}
	for _, a := range got {
		driverNode := a.DriverID - 1 // driver ID = node ID + 1
		if clusterOf[driverNode] != clusterOf[a.Request.PickupNode] {
			t.Errorf("driver from cluster %d matched to request in cluster %d",
				clusterOf[driverNode], clusterOf[a.Request.PickupNode])
		}
	}
}

// Every request has a driver on its own node, so both policies match all 18.
func TestRegionShardedMatchesBatchCount(t *testing.T) {
	centers := [][2]float64{{37.77, -122.42}, {34.05, -118.24}, {40.71, -74.0}}
	g, clusters := clusteredGraph(centers, 6)

	ctx := buildCtx(g, clusters)
	batch := matchBatch(ctx, ctx.Pending, 5, 0, 0)
	sharded := NewRegionShardedBatchPolicy(0).Match(buildCtx(g, clusters))

	if len(batch) != 18 || len(sharded) != 18 {
		t.Errorf("want 18 assignments from each policy, got batch=%d sharded=%d", len(batch), len(sharded))
	}
}

// benchPolicy times one Match over clusters*perCluster requests. The sharded
// policy solves one small Hungarian per cluster instead of one large one.
func benchPolicy(b *testing.B, sharded bool, clusters, perCluster int) {
	centers := make([][2]float64, clusters)
	for i := range centers {
		// Half a degree apart puts each cluster in its own r5 cell.
		centers[i] = [2]float64{37.0 + float64(i)*0.5, -122.0 + float64(i)*0.5}
	}
	g, cl := clusteredGraph(centers, perCluster)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ctx := buildCtx(g, cl) // policies consume drivers from the index
		b.StartTimer()
		if sharded {
			NewRegionShardedBatchPolicy(0).Match(ctx)
		} else {
			(&BatchPolicy{Window: 0, CandidatesPerRequest: 5}).Match(ctx)
		}
	}
}

func BenchmarkBatch_30x12(b *testing.B)         { benchPolicy(b, false, 30, 12) }
func BenchmarkRegionSharded_30x12(b *testing.B) { benchPolicy(b, true, 30, 12) }

// At 40x25 the cubic Hungarian solve outweighs the shared A* candidate cost:
// one 1000x1000 matrix against forty 25x25 ones.
func BenchmarkBatch_40x25(b *testing.B)         { benchPolicy(b, false, 40, 25) }
func BenchmarkRegionSharded_40x25(b *testing.B) { benchPolicy(b, true, 40, 25) }
