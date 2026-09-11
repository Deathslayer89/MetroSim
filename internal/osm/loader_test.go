package osm

import (
	"os"
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

const cityPBF = "../../data/osm/city.osm.pbf"

func loadCityOrSkip(t *testing.T) *graph.Graph {
	t.Helper()
	if _, err := os.Stat(cityPBF); os.IsNotExist(err) {
		t.Skipf("skipping: %s not present. Run data/osm/fetch.sh first.", cityPBF)
	}
	g, err := LoadPBF(cityPBF)
	if err != nil {
		t.Fatalf("LoadPBF: %v", err)
	}
	return g
}

func TestLoadPBFMeetsScaleBar(t *testing.T) {
	g := loadCityOrSkip(t)

	if g.NodeCount() < 5000 {
		t.Errorf("need >=5000 nodes, got %d", g.NodeCount())
	}
	if g.EdgeCount() < 10000 {
		t.Errorf("need >=10000 edges, got %d", g.EdgeCount())
	}
	t.Logf("loaded SF graph: %d nodes, %d edges", g.NodeCount(), g.EdgeCount())
}

func TestParseMaxspeed(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"", 0},
		{"none", 0},
		{"signals", 0},
		{"50", 50.0 / 3.6},
		{"50 km/h", 50.0 / 3.6},
		{"50km/h", 50.0 / 3.6},
		{"60 kph", 60.0 / 3.6},
		{"-20", 0}, // non-positive: use the road-class default
		{"0", 0},
		{"RU:urban", 0},
		{"30 mph", 30 * 0.44704},
		{"35mph", 35 * 0.44704},
	}
	for _, c := range cases {
		got := parseMaxspeed(c.in)
		if got != c.want {
			t.Errorf("parseMaxspeed(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
