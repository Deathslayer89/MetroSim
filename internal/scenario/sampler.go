package scenario

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// nodeSampler draws node IDs from a uniform background over the whole graph
// plus hotspots. A hotspot of weight w carries w times the background's total
// mass, spread evenly over the nodes inside its radius.
type nodeSampler struct {
	nodes []int
	cumWt []float64
	total float64
}

// buildNodeSampler fails on a hotspot that covers no node, which would
// otherwise hand its share to the others without a word.
func buildNodeSampler(g *graph.Graph, hotspots []Hotspot) (*nodeSampler, error) {
	ids := make([]int, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	w := make([]float64, len(ids))
	for i := range w {
		w[i] = 1
	}
	for _, h := range hotspots {
		var inside []int
		for i, id := range ids {
			n := g.Nodes[id]
			if haversineMeters(n.Lat, n.Lon, h.Lat, h.Lon) <= h.RadiusM {
				inside = append(inside, i)
			}
		}
		if len(inside) == 0 {
			return nil, fmt.Errorf("hotspot %q at (%g, %g) covers no road nodes within %g m", h.Name, h.Lat, h.Lon, h.RadiusM)
		}
		per := h.Weight * float64(len(ids)) / float64(len(inside))
		for _, i := range inside {
			w[i] += per
		}
	}

	cum := make([]float64, len(ids))
	var sum float64
	for i := range ids {
		sum += w[i]
		cum[i] = sum
	}
	return &nodeSampler{nodes: ids, cumWt: cum, total: sum}, nil
}

func (s *nodeSampler) Sample(rng *rand.Rand) int {
	if s.total <= 0 {
		return s.nodes[rng.Intn(len(s.nodes))]
	}
	r := rng.Float64() * s.total
	i := sort.SearchFloat64s(s.cumWt, r)
	if i >= len(s.nodes) {
		i = len(s.nodes) - 1
	}
	return s.nodes[i]
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6371000.0
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dlat := (lat2 - lat1) * math.Pi / 180
	dlon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
