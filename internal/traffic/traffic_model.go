package traffic

import (
	"math"
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

// TrafficModel counts vehicles per edge and the congested travel times that
// follow. Both maps hold occupied edges only, so per-tick work scales with the
// fleet rather than the graph.
type TrafficModel struct {
	graph            *graph.Graph
	edgeDensities    map[int]int     // vehicles per occupied edge
	congested        map[int]float64 // travel time of edges slower than free flow
	reported         map[int]float64 // travel time when last reported changed; free flow if absent
	edgeCapacities   map[int]float64 // vehicles each edge holds
	congestionParams CongestionParams
	mu               sync.RWMutex
}

func NewTrafficModel(g *graph.Graph, params CongestionParams) *TrafficModel {
	tm := &TrafficModel{
		graph:            g,
		edgeDensities:    make(map[int]int),
		congested:        make(map[int]float64),
		reported:         make(map[int]float64),
		edgeCapacities:   make(map[int]float64),
		congestionParams: params,
	}
	// One vehicle per 7 m of lane: a 5 m car plus a 2 m gap.
	for edgeID, edge := range g.Edges {
		tm.edgeCapacities[edgeID] = float64(edge.Lanes) * edge.Length / 7.0
	}
	return tm
}

// UpdateDensities recounts vehicles per edge from their current positions.
func (tm *TrafficModel) UpdateDensities(vehicles []*agent.Vehicle) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.edgeDensities = make(map[int]int, len(tm.edgeDensities))
	for _, vehicle := range vehicles {
		if vehicle.CurrentEdge != nil {
			tm.edgeDensities[vehicle.CurrentEdge.ID]++
		}
	}
}

// ComputeEdgeWeights recomputes travel times on occupied edges and returns the
// edges whose time has moved more than 10% since it was last reported, which is
// what triggers replanning. Measuring from the last report rather than the last
// tick means a jam that grows a little every tick still gets reported.
func (tm *TrafficModel) ComputeEdgeWeights() map[int]float64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	newCongested := make(map[int]float64, len(tm.edgeDensities))
	for id, density := range tm.edgeDensities {
		edge := tm.graph.Edges[id]
		if edge == nil {
			continue
		}
		if w := tm.calculateCongestionWeight(edge, density); w > edge.BaseWeight*1.0001 {
			newCongested[id] = w
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

func (tm *TrafficModel) calculateCongestionWeight(edge *graph.Edge, density int) float64 {
	return edge.BaseWeight * tm.congestionParams.factor(density, tm.edgeCapacities[edge.ID])
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

// GetCongestionFactor returns an edge's current multiplier, 1.0 when empty.
func (tm *TrafficModel) GetCongestionFactor(edgeID int) float64 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.congestionFactor(edgeID)
}

// congestionFactor needs tm.mu held.
func (tm *TrafficModel) congestionFactor(edgeID int) float64 {
	return tm.congestionParams.factor(tm.edgeDensities[edgeID], tm.edgeCapacities[edgeID])
}

func (tm *TrafficModel) GetEdgeDensity(edgeID int) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.edgeDensities[edgeID]
}

func (tm *TrafficModel) GetEdgeCapacity(edgeID int) float64 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.edgeCapacities[edgeID]
}

// CongestionStats summarizes the occupied edges.
type CongestionStats struct {
	TotalEdges     int
	CongestedEdges int     // over half their capacity
	AvgCongestion  float64 // mean factor over occupied edges
	MaxCongestion  float64
	MaxCongestedID int // -1 when no edge is occupied
}

// GetStats looks at occupied edges only.
func (tm *TrafficModel) GetStats() CongestionStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	total := len(tm.graph.Edges)
	stats := CongestionStats{
		TotalEdges:     total,
		MaxCongestion:  1.0,
		MaxCongestedID: -1,
	}

	// Averaging over every edge would dilute any pileup to about 1.0.
	sumFactor := 0.0
	occupied := 0
	for edgeID, density := range tm.edgeDensities {
		capacity := tm.edgeCapacities[edgeID]
		if capacity <= 0 {
			continue
		}
		occupied++
		factor := tm.congestionFactor(edgeID)
		sumFactor += factor
		if float64(density)/capacity > 0.5 {
			stats.CongestedEdges++
		}
		if factor > stats.MaxCongestion {
			stats.MaxCongestion = factor
			stats.MaxCongestedID = edgeID
		}
	}
	if occupied > 0 {
		stats.AvgCongestion = sumFactor / float64(occupied)
	} else {
		stats.AvgCongestion = 1.0
	}
	return stats
}
