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
	way.speedLimit, way.lanes = 10, 1
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
