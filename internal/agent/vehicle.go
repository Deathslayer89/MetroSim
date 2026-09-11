package agent

import (
	"fmt"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

type VehicleState int

// Whether an enroute vehicle is heading to a pickup lives in the dispatcher's
// ride state, not here. A repositioning vehicle drives with no rider and stays
// available to the dispatcher.
const (
	StateIdle VehicleState = iota
	StateEnroute
	StateRepositioning
)

func (s VehicleState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateEnroute:
		return "enroute"
	case StateRepositioning:
		return "repositioning"
	default:
		return "unknown"
	}
}

type Vehicle struct {
	ID          int
	CurrentNode int
	CurrentEdge *graph.Edge
	Progress    float64 // 0.0 to 1.0 along current edge
	Route       []*graph.Edge
	RouteIndex  int // index of current edge in route
	State       VehicleState
	Destination int

	graph       *graph.Graph
	pathPlanner *pathfinding.PathPlanner
}

func NewVehicle(id int, startNode int, g *graph.Graph, planner *pathfinding.PathPlanner) *Vehicle {
	return &Vehicle{
		ID:          id,
		CurrentNode: startNode,
		State:       StateIdle,
		graph:       g,
		pathPlanner: planner,
	}
}

// SetDestination only records the target. PlanRoute sets Enroute on success,
// so a failed plan leaves the vehicle Idle and matchable.
func (v *Vehicle) SetDestination(nodeID int) {
	v.Destination = nodeID
}

// Abort drops the route and makes the vehicle Idle. Mid-edge, it jumps to the
// edge's end node so it sits on a real node for rematching.
func (v *Vehicle) Abort() {
	if v.CurrentEdge != nil {
		v.CurrentNode = v.CurrentEdge.ToNode
		v.CurrentEdge = nil
	}
	v.Route = nil
	v.RouteIndex = 0
	v.Progress = 0
	v.State = StateIdle
}

// PlanRoute plans on free-flow travel times.
func (v *Vehicle) PlanRoute() error {
	return v.PlanRouteWithWeights(nil)
}

// PlanRouteWithWeights routes to v.Destination on the given travel times, nil
// meaning free flow. On error the vehicle's state and route are unchanged.
func (v *Vehicle) PlanRouteWithWeights(weights map[int]float64) error {
	startNode := v.CurrentNode
	if v.CurrentEdge != nil {
		startNode = v.CurrentEdge.ToNode
	}

	var route []*graph.Edge
	if startNode != v.Destination {
		r, err := v.pathPlanner.FindPathWithWeights(startNode, v.Destination, weights)
		if err != nil {
			return fmt.Errorf("route vehicle %d: %w", v.ID, err)
		}
		route = r
	}
	// Mid-edge, the vehicle finishes the edge it's on first.
	if v.CurrentEdge != nil {
		route = append([]*graph.Edge{v.CurrentEdge}, route...)
	}

	v.Route = route
	v.RouteIndex = 0
	if len(route) == 0 {
		v.State = StateIdle
	} else {
		v.State = StateEnroute
	}
	return nil
}

// Reposition drives the vehicle to node with no rider. It stays available on
// the way and turns Idle when it arrives.
func (v *Vehicle) Reposition(node int, weights map[int]float64) error {
	v.SetDestination(node)
	if err := v.PlanRouteWithWeights(weights); err != nil {
		return err
	}
	if v.State == StateEnroute {
		v.State = StateRepositioning
	}
	return nil
}

// Move advances at free-flow speed.
func (v *Vehicle) Move(deltaTime float64) {
	v.MoveWithWeights(deltaTime, nil)
}

// MoveWithWeights spends deltaTime seconds traveling at each edge's travel time
// from weights, free flow when absent. Leftover time carries into the next edge.
func (v *Vehicle) MoveWithWeights(deltaTime float64, weights map[int]float64) {
	if !v.moving() {
		return
	}

	if len(v.Route) == 0 {
		if v.CurrentNode == v.Destination {
			v.State = StateIdle
		}
		return
	}

	if v.CurrentEdge == nil && v.RouteIndex < len(v.Route) {
		v.CurrentEdge = v.Route[v.RouteIndex]
		v.Progress = 0.0
	}

	if v.CurrentEdge == nil {
		return
	}

	remaining := deltaTime
	for remaining > 0 {
		edgeTime := edgeTravelTime(v.CurrentEdge, weights)
		if edgeTime <= 0 {
			v.Progress = 1.0 // zero-length edge
		}
		timeLeftOnEdge := (1.0 - v.Progress) * edgeTime
		if remaining < timeLeftOnEdge {
			v.Progress += remaining / edgeTime
			return
		}

		remaining -= timeLeftOnEdge
		v.CurrentNode = v.CurrentEdge.ToNode
		v.Progress = 0.0
		v.RouteIndex++

		if v.CurrentNode == v.Destination {
			v.CurrentEdge = nil
			v.Route = nil
			v.State = StateIdle
			return
		}
		if v.RouteIndex < len(v.Route) {
			v.CurrentEdge = v.Route[v.RouteIndex]
		} else {
			// The route ran out short of the destination.
			v.CurrentEdge = nil
			v.State = StateIdle
			return
		}
	}
}

// edgeTravelTime is weights[e.ID] when set, else free flow.
func edgeTravelTime(e *graph.Edge, weights map[int]float64) float64 {
	if w, ok := weights[e.ID]; ok {
		return w
	}
	return e.BaseWeight
}

// GetPosition interpolates along the current edge.
func (v *Vehicle) GetPosition() (lat, lon float64, err error) {
	if v.CurrentEdge == nil {
		node, err := v.graph.GetNode(v.CurrentNode)
		if err != nil {
			return 0, 0, err
		}
		return node.Lat, node.Lon, nil
	}

	fromNode, err := v.graph.GetNode(v.CurrentEdge.FromNode)
	if err != nil {
		return 0, 0, err
	}

	toNode, err := v.graph.GetNode(v.CurrentEdge.ToNode)
	if err != nil {
		return 0, 0, err
	}

	lat = fromNode.Lat + (toNode.Lat-fromNode.Lat)*v.Progress
	lon = fromNode.Lon + (toNode.Lon-fromNode.Lon)*v.Progress

	return lat, lon, nil
}

// VehicleSnapshot is a copy of a vehicle's visible state, safe to share.
type VehicleSnapshot struct {
	ID          int
	Lat         float64
	Lon         float64
	State       VehicleState
	CurrentNode int
}

// Snapshot copies the vehicle. Vehicle has no lock of its own, so the caller
// must hold the engine's.
func (v *Vehicle) Snapshot() (VehicleSnapshot, bool) {
	lat, lon, err := v.GetPosition()
	if err != nil {
		return VehicleSnapshot{}, false
	}
	return VehicleSnapshot{
		ID:          v.ID,
		Lat:         lat,
		Lon:         lon,
		State:       v.State,
		CurrentNode: v.CurrentNode,
	}, true
}

func (v *Vehicle) IsIdle() bool {
	return v.State == StateIdle
}

func (v *Vehicle) IsEnroute() bool {
	return v.State == StateEnroute
}

// IsAvailable reports whether the dispatcher may assign the vehicle.
func (v *Vehicle) IsAvailable() bool {
	return v.State == StateIdle || v.State == StateRepositioning
}

func (v *Vehicle) moving() bool {
	return v.State == StateEnroute || v.State == StateRepositioning
}

func (v *Vehicle) HasReachedDestination() bool {
	return v.State == StateIdle && v.CurrentNode == v.Destination
}

// MaybeReplan replans when an edge in changed is still ahead on the route and
// another route is at least 10% cheaper. It runs at most one A* search.
func (v *Vehicle) MaybeReplan(changed, weights map[int]float64) bool {
	if len(changed) == 0 {
		return false
	}
	if !v.changedAhead(changed) {
		return false
	}
	return v.replan(weights)
}

func (v *Vehicle) changedAhead(changed map[int]float64) bool {
	if !v.moving() {
		return false
	}
	for i := v.RouteIndex; i < len(v.Route); i++ {
		if _, ok := changed[v.Route[i].ID]; ok {
			return true
		}
	}
	return false
}

// replan switches to a route at least 10% cheaper than the rest of the current
// one. Mid-edge, the current edge stays at the front and is left out of both
// costs. It reports whether the route changed.
func (v *Vehicle) replan(weights map[int]float64) bool {
	startNode := v.CurrentNode
	remainingFrom := v.RouteIndex
	if v.CurrentEdge != nil {
		startNode = v.CurrentEdge.ToNode
		remainingFrom = v.RouteIndex + 1
	}
	if startNode == v.Destination {
		return false
	}

	currentCost := routeCost(v.Route[remainingFrom:], weights)

	newRoute, err := v.pathPlanner.FindPathWithWeights(startNode, v.Destination, weights)
	if err != nil {
		return false
	}
	if routeCost(newRoute, weights) >= currentCost*0.9 {
		return false
	}

	if v.CurrentEdge != nil {
		v.Route = append([]*graph.Edge{v.CurrentEdge}, newRoute...)
	} else {
		v.Route = newRoute
	}
	v.RouteIndex = 0
	return true
}

func routeCost(edges []*graph.Edge, weights map[int]float64) float64 {
	cost := 0.0
	for _, e := range edges {
		cost += edgeTravelTime(e, weights)
	}
	return cost
}
