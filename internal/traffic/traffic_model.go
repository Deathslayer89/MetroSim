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
	edgeCapacities   map[int]float64 // vehicles each edge holds
	congestionParams CongestionParams
	mu               sync.RWMutex
}

func NewTrafficModel(g *graph.Graph, params CongestionParams) *TrafficModel {
	tm := &TrafficModel{
		graph:            g,
		edgeDensities:    make(map[int]int),
		congested:        make(map[int]float64),
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
// edges whose time moved by more than 10%, which is what triggers replanning.
func (tm *TrafficModel) ComputeEdgeWeights() map[int]float64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	changed := make(map[int]float64)
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

	baseOf := func(id int) float64 {
		if e := tm.graph.Edges[id]; e != nil {
			return e.BaseWeight
		}
		return 0
	}
	// Edges congested last tick, including ones that just cleared.
	for id, oldW := range tm.congested {
		newW, ok := newCongested[id]
		if !ok {
			newW = baseOf(id)
		}
		if oldW > 0 && math.Abs(newW-oldW)/oldW > 0.1 {
			changed[id] = newW
		}
	}
	// Newly congested edges, compared with free flow.
	for id, newW := range newCongested {
		if _, was := tm.congested[id]; was {
			continue
		}
		if base := baseOf(id); base > 0 && math.Abs(newW-base)/base > 0.1 {
			changed[id] = newW
		}
	}

	tm.congested = newCongested
	return changed
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
