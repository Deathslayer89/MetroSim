package traffic

import (
	"math"
	"sort"
	"sync"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// CongestionParams shape the BPR curve: an edge's travel time is its free-flow
// time times 1 + Alpha*(density/capacity)^Beta, capped at MaxFactor if set.
type CongestionParams struct {
	Alpha     float64
	Beta      float64
	MaxFactor float64
}

func (p CongestionParams) factor(density int, capacity float64) float64 {
	if capacity <= 0 {
		return 1
	}
	f := 1 + p.Alpha*math.Pow(float64(density)/capacity, p.Beta)
	if p.MaxFactor > 0 && f > p.MaxFactor {
		f = p.MaxFactor
	}
	return f
}

// DefaultCongestionParams returns the textbook BPR parameters.
func DefaultCongestionParams() CongestionParams {
	return CongestionParams{
		Alpha: 0.15,
		Beta:  4.0,
	}
}

// DemoCongestionParams is steeper than textbook BPR so a few hundred vehicles
// cause visible congestion, capped at 5x so dense scenes don't lock up.
func DemoCongestionParams() CongestionParams {
	return CongestionParams{
		Alpha:     1.0,
		Beta:      2.0,
		MaxFactor: 5.0,
	}
}

// TrafficModel sets travel times from density per link, the road between two
// intersections, since most OSM segments are too short to hold a car. A link's
// density leaves one car out: a car isn't held up by itself.
type TrafficModel struct {
	graph            *graph.Graph
	linkOf           map[int]int     // edge ID -> its link
	links            [][]int         // each link's edge IDs, in driving order
	storage          []float64       // cars each link holds
	cars             map[int]int     // cars on each occupied link
	congested        map[int]float64 // travel time of edges slower than free flow
	reported         map[int]float64 // travel time when last reported changed; free flow if absent
	congestionParams CongestionParams
	mu               sync.RWMutex
}

func NewTrafficModel(g *graph.Graph, params CongestionParams) *TrafficModel {
	tm := &TrafficModel{
		graph:            g,
		cars:             make(map[int]int),
		congested:        make(map[int]float64),
		reported:         make(map[int]float64),
		congestionParams: params,
	}
	tm.linkOf, tm.links = buildLinks(g)
	tm.storage = make([]float64, len(tm.links))
	for l, ids := range tm.links {
		for _, id := range ids {
			// One vehicle per 7 m of lane: a 5 m car plus a 2 m gap.
			e := g.Edges[id]
			tm.storage[l] += float64(e.Lanes) * e.Length / 7.0
		}
	}
	return tm
}

// buildLinks chains edges through nodes where the road neither branches nor
// merges, in edge-ID order so the result doesn't depend on map order.
func buildLinks(g *graph.Graph) (map[int]int, [][]int) {
	in := make(map[int][]*graph.Edge, len(g.Nodes))
	ids := make([]int, 0, len(g.Edges))
	for id, e := range g.Edges {
		in[e.ToNode] = append(in[e.ToNode], e)
		ids = append(ids, id)
	}
	sort.Ints(ids)

	// continues returns the one edge carrying on from e, or nil at a junction.
	continues := func(e *graph.Edge) *graph.Edge {
		var out *graph.Edge
		for _, f := range g.Adjacency[e.ToNode] {
			if f.ToNode == e.FromNode {
				continue
			}
			if out != nil {
				return nil // the road branches
			}
			out = f
		}
		if out == nil {
			return nil
		}
		for _, f := range in[e.ToNode] {
			if f != e && f.FromNode != out.ToNode {
				return nil // another road merges in
			}
		}
		return out
	}
	next := make(map[int]int, len(ids))
	hasPrev := make(map[int]bool, len(ids))
	for _, id := range ids {
		if f := continues(g.Edges[id]); f != nil {
			next[id] = f.ID
			hasPrev[f.ID] = true
		}
	}

	linkOf := make(map[int]int, len(ids))
	var links [][]int
	walk := func(id int) {
		l := len(links)
		var chain []int
		for {
			if _, seen := linkOf[id]; seen {
				break
			}
			linkOf[id] = l
			chain = append(chain, id)
			n, ok := next[id]
			if !ok {
				break
			}
			id = n
		}
		links = append(links, chain)
	}
	for _, id := range ids {
		if !hasPrev[id] {
			walk(id)
		}
	}
	// What's left runs in loops with no intersection on them.
	for _, id := range ids {
		if _, seen := linkOf[id]; !seen {
			walk(id)
		}
	}
	return linkOf, links
}

// UpdateDensities recounts vehicles per link from their current positions.
func (tm *TrafficModel) UpdateDensities(vehicles []*agent.Vehicle) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.cars = make(map[int]int, len(tm.cars))
	for _, vehicle := range vehicles {
		if vehicle.CurrentEdge == nil {
			continue
		}
		if l, ok := tm.linkOf[vehicle.CurrentEdge.ID]; ok {
			tm.cars[l]++
		}
	}
}

// ComputeEdgeWeights recomputes occupied links and returns the edges whose time
// moved more than 10% since last reported, which triggers replanning. Counting
// from the last report catches a jam that grows a little every tick.
func (tm *TrafficModel) ComputeEdgeWeights() map[int]float64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	newCongested := make(map[int]float64, len(tm.congested))
	for l := range tm.cars {
		f := tm.linkFactor(l)
		if f <= 1.0001 {
			continue
		}
		for _, id := range tm.links[l] {
			newCongested[id] = tm.graph.Edges[id].BaseWeight * f
		}
	}
	tm.congested = newCongested

	changed := make(map[int]float64)
	for id, w := range newCongested {
		tm.noteWeight(id, w, changed)
	}
	// Edges reported congested earlier that have since cleared.
	for id := range tm.reported {
		if _, ok := newCongested[id]; !ok {
			tm.noteWeight(id, tm.graph.Edges[id].BaseWeight, changed)
		}
	}
	return changed
}

// noteWeight adds id to changed when w is more than 10% from the weight last
// reported for it. It needs tm.mu held.
func (tm *TrafficModel) noteWeight(id int, w float64, changed map[int]float64) {
	base := tm.graph.Edges[id].BaseWeight
	last, ok := tm.reported[id]
	if !ok {
		last = base
	}
	free := w <= base*1.0001
	switch {
	case last > 0 && math.Abs(w-last)/last > 0.1:
		changed[id] = w
		if free {
			delete(tm.reported, id)
		} else {
			tm.reported[id] = w
		}
	case free:
		delete(tm.reported, id) // cleared by less than 10%: nothing to replan for
	}
}

// linkFactor is link l's slowdown from the cars on it other than one, since a
// car isn't held up by itself. It needs tm.mu held.
func (tm *TrafficModel) linkFactor(l int) float64 {
	return tm.congestionParams.factor(max(0, tm.cars[l]-1), tm.storage[l])
}

// GetEdgeWeight returns an edge's current travel time: its congested value if
// any, else free-flow BaseWeight (+Inf if the edge doesn't exist).
func (tm *TrafficModel) GetEdgeWeight(edgeID int) float64 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if w, ok := tm.congested[edgeID]; ok {
		return w
	}
	if e := tm.graph.Edges[edgeID]; e != nil {
		return e.BaseWeight
	}
	return math.Inf(1)
}

// GetEdgeWeights returns the congested-edge overrides (sparse). Callers fall
// back to free-flow BaseWeight for edges not present.
func (tm *TrafficModel) GetEdgeWeights() map[int]float64 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	out := make(map[int]float64, len(tm.congested))
	for id, w := range tm.congested {
		out[id] = w
	}
	return out
}

// GetCongestionFactor returns an edge's current multiplier, 1.0 while its link
// holds one car or none.
func (tm *TrafficModel) GetCongestionFactor(edgeID int) float64 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	l, ok := tm.linkOf[edgeID]
	if !ok {
		return 1
	}
	return tm.linkFactor(l)
}

// GetEdgeDensity returns the vehicles on the edge's link.
func (tm *TrafficModel) GetEdgeDensity(edgeID int) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	l, ok := tm.linkOf[edgeID]
	if !ok {
		return 0
	}
	return tm.cars[l]
}

// GetEdgeCapacity returns how many vehicles the edge's link holds.
func (tm *TrafficModel) GetEdgeCapacity(edgeID int) float64 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	l, ok := tm.linkOf[edgeID]
	if !ok {
		return 0
	}
	return tm.storage[l]
}

// CongestionStats summarizes the edges of occupied links.
type CongestionStats struct {
	TotalEdges     int
	CongestedEdges int     // on links over half full
	AvgCongestion  float64 // mean factor over edges of occupied links
	MaxCongestion  float64
	MaxCongestedID int // an edge of the slowest link; -1 when none is occupied
}

// GetStats looks at occupied links only.
func (tm *TrafficModel) GetStats() CongestionStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats := CongestionStats{
		TotalEdges:     len(tm.graph.Edges),
		MaxCongestion:  1.0,
		MaxCongestedID: -1,
	}

	// Averaging over every edge would dilute any pileup to about 1.0.
	sumFactor := 0.0
	occupied := 0
	for l, n := range tm.cars {
		if tm.storage[l] <= 0 {
			continue
		}
		edges := len(tm.links[l])
		factor := tm.linkFactor(l)
		occupied += edges
		sumFactor += factor * float64(edges)
		if float64(n)/tm.storage[l] > 0.5 {
			stats.CongestedEdges += edges
		}
		if factor > stats.MaxCongestion {
			stats.MaxCongestion = factor
			stats.MaxCongestedID = tm.links[l][0]
		}
	}
	if occupied > 0 {
		stats.AvgCongestion = sumFactor / float64(occupied)
	} else {
		stats.AvgCongestion = 1.0
	}
	return stats
}
