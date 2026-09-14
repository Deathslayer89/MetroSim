package graph

import (
	"fmt"
	"math"
)

type Node struct {
	ID  int
	Lat float64
	Lon float64
}

// Edge is a one-way road segment.
type Edge struct {
	ID         int
	FromNode   int
	ToNode     int
	Length     float64 // meters
	Lanes      int
	SpeedLimit float64 // m/s
	BaseWeight float64 // base travel time in seconds
}

type Graph struct {
	Nodes     map[int]*Node
	Edges     map[int]*Edge
	Adjacency map[int][]*Edge // nodeID -> outgoing edges
}

func NewGraph() *Graph {
	return &Graph{
		Nodes:     make(map[int]*Node),
		Edges:     make(map[int]*Edge),
		Adjacency: make(map[int][]*Edge),
	}
}

func (g *Graph) AddNode(node *Node) {
	g.Nodes[node.ID] = node
	if _, exists := g.Adjacency[node.ID]; !exists {
		g.Adjacency[node.ID] = make([]*Edge, 0)
	}
}

// AddEdge adds a directed edge. An unset BaseWeight is derived from length and
// speed; a non-positive speed makes the edge unusable rather than NaN.
func (g *Graph) AddEdge(edge *Edge) {
	g.Edges[edge.ID] = edge
	g.Adjacency[edge.FromNode] = append(g.Adjacency[edge.FromNode], edge)

	if edge.BaseWeight == 0 {
		if edge.SpeedLimit > 0 {
			edge.BaseWeight = edge.Length / edge.SpeedLimit
		} else {
			edge.BaseWeight = math.Inf(1)
		}
	}
}

func (g *Graph) GetNeighbors(nodeID int) []*Edge {
	return g.Adjacency[nodeID]
}

// GetEdgeWeight returns an edge's free-flow travel time, or +Inf if unknown.
func (g *Graph) GetEdgeWeight(edgeID int) float64 {
	if edge, exists := g.Edges[edgeID]; exists {
		return edge.BaseWeight
	}
	return math.Inf(1)
}

func (g *Graph) GetNode(nodeID int) (*Node, error) {
	if node, exists := g.Nodes[nodeID]; exists {
		return node, nil
	}
	return nil, fmt.Errorf("node %d not found", nodeID)
}

func (g *Graph) GetEdge(edgeID int) (*Edge, error) {
	if edge, exists := g.Edges[edgeID]; exists {
		return edge, nil
	}
	return nil, fmt.Errorf("edge %d not found", edgeID)
}

// EuclideanDistance returns the distance between two nodes in meters using an
// equirectangular approximation (longitude scaled by cos(mean latitude)).
// Accurate to well under 1% at city scale.
func EuclideanDistance(n1, n2 *Node) float64 {
	const metersPerDegree = 111000.0

	dx := (n1.Lon - n2.Lon) * metersPerDegree * math.Cos((n1.Lat+n2.Lat)/2*math.Pi/180)
	dy := (n1.Lat - n2.Lat) * metersPerDegree

	return math.Sqrt(dx*dx + dy*dy)
}

// HaversineMeters is the great-circle distance between two points, in meters.
func HaversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6371000.0
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dlat := (lat2 - lat1) * math.Pi / 180
	dlon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func (g *Graph) NodeCount() int {
	return len(g.Nodes)
}

func (g *Graph) EdgeCount() int {
	return len(g.Edges)
}
