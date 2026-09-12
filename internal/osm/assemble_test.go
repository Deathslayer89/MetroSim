package osm

import (
	"testing"

	pmosm "github.com/paulmach/osm"
)

// Graph node IDs follow sorted OSM IDs, so a seeded run gets the same graph
// every time. Map iteration order would shuffle them.
func TestAssembleNumbersNodesByOSMID(t *testing.T) {
	nodes := make(map[pmosm.NodeID]*pmosm.Node)
	var way wayRec
	for i := 0; i < 50; i++ {
		id := pmosm.NodeID(1000 - 7*i)
		nodes[id] = &pmosm.Node{ID: id, Lat: 37.77 + float64(i)*0.001, Lon: -122.42}
		way.nodes = append(way.nodes, id)
	}
	way.speedLimit, way.lanes, way.lanesBack = 10, 1, 1
	smallest := nodes[pmosm.NodeID(1000-7*49)]

	for run := 0; run < 5; run++ {
		g, err := assemble([]wayRec{way}, nodes)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		if n := g.Nodes[0]; n.Lat != smallest.Lat {
			t.Fatalf("run %d: graph node 0 is at lat %v, want the smallest OSM ID at %v", run, n.Lat, smallest.Lat)
		}
	}
}

// A two-way way emits one edge each way, each with its own direction's lanes.
func TestAssembleGivesEachDirectionItsLanes(t *testing.T) {
	nodes := map[pmosm.NodeID]*pmosm.Node{
		1: {ID: 1, Lat: 37.770, Lon: -122.42},
		2: {ID: 2, Lat: 37.771, Lon: -122.42},
	}
	way := wayRec{nodes: []pmosm.NodeID{1, 2}, speedLimit: 10, lanes: 2, lanesBack: 1}
	g, err := assemble([]wayRec{way}, nodes)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if f, b := g.Edges[0].Lanes, g.Edges[1].Lanes; f != 2 || b != 1 {
		t.Errorf("want 2 lanes forward and 1 back, got %d and %d", f, b)
	}
}
