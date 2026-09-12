package osm

import (
	"os"
	"testing"

	pmosm "github.com/paulmach/osm"

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

func TestWayLanesSplitsTwoWayRoads(t *testing.T) {
	tags := func(kv ...string) pmosm.Tags {
		var out pmosm.Tags
		for i := 0; i+1 < len(kv); i += 2 {
			out = append(out, pmosm.Tag{Key: kv[i], Value: kv[i+1]})
		}
		return out
	}
	cases := []struct {
		tags          pmosm.Tags
		oneway        bool
		forward, back int
	}{
		{tags("lanes", "4"), false, 2, 2},
		{tags("lanes", "3"), false, 2, 1},
		{tags("lanes", "3", "lanes:forward", "1", "lanes:backward", "2"), false, 1, 2},
		{tags("lanes", "1"), false, 1, 1},
		{tags(), false, 1, 1},
		{tags("lanes", "3"), true, 3, 0},
		{tags(), true, 1, 0},
	}
	for _, c := range cases {
		f, b := wayLanes(c.tags, c.oneway)
		if f != c.forward || b != c.back {
			t.Errorf("wayLanes(%v, oneway=%v) = %d, %d; want %d, %d", c.tags, c.oneway, f, b, c.forward, c.back)
		}
	}
}
