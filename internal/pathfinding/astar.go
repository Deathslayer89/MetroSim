package pathfinding

import (
	"container/heap"
	"fmt"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// Heuristic estimates travel time in seconds between two nodes.
type Heuristic func(from, to *graph.Node) float64

type PathPlanner struct {
	graph     *graph.Graph
	heuristic Heuristic
}

// DefaultHeuristicWeight is the weight NewPathPlanner uses. Routes cost at most
// this many times the optimum; a higher weight searches fewer nodes.
const DefaultHeuristicWeight = 3.0

// NewPathPlanner builds an A* planner over g. A nil heuristic means
// WeightedTimeHeuristic(g, DefaultHeuristicWeight).
func NewPathPlanner(g *graph.Graph, heuristic Heuristic) *PathPlanner {
	if heuristic == nil {
		heuristic = WeightedTimeHeuristic(g, DefaultHeuristicWeight)
	}
	return &PathPlanner{graph: g, heuristic: heuristic}
}

// AdmissibleTimeHeuristic is straight-line distance over the graph's highest
// speed limit. It never overestimates, so A* returns optimal routes.
func AdmissibleTimeHeuristic(g *graph.Graph) Heuristic { return WeightedTimeHeuristic(g, 1) }

// WeightedTimeHeuristic scales the admissible bound by w. A* then expands fewer
// nodes and returns routes costing at most w times the optimum.
func WeightedTimeHeuristic(g *graph.Graph, w float64) Heuristic {
	maxSpeed := 1.0 // avoids dividing by zero on an edgeless graph
	for _, e := range g.Edges {
		if e.SpeedLimit > maxSpeed {
			maxSpeed = e.SpeedLimit
		}
	}
	return func(from, to *graph.Node) float64 {
		return w * graph.EuclideanDistance(from, to) / maxSpeed
	}
}

type pathNode struct {
	nodeID   int
	gCost    float64 // cost from start to this node
	fCost    float64 // gCost + heuristic
	parent   *pathNode
	edgeUsed *graph.Edge // edge used to reach this node
	index    int         // index in the priority queue
}

type priorityQueue []*pathNode

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	return pq[i].fCost < pq[j].fCost
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	node := x.(*pathNode)
	node.index = n
	*pq = append(*pq, node)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	node := old[n-1]
	old[n-1] = nil
	node.index = -1
	*pq = old[0 : n-1]
	return node
}

// FindPath routes on free-flow travel times.
func (pp *PathPlanner) FindPath(startID, goalID int) ([]*graph.Edge, error) {
	return pp.FindPathWithWeights(startID, goalID, nil)
}

// reconstructPath walks parent links from goal back to start, then reverses.
func reconstructPath(goalNode *pathNode) []*graph.Edge {
	var path []*graph.Edge
	for current := goalNode; current.parent != nil; current = current.parent {
		path = append(path, current.edgeUsed)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// FindPathWithWeights routes on weights, using free flow for edges not in it.
func (pp *PathPlanner) FindPathWithWeights(startID, goalID int, weights map[int]float64) ([]*graph.Edge, error) {
	startNode, err := pp.graph.GetNode(startID)
	if err != nil {
		return nil, fmt.Errorf("start node not found: %w", err)
	}

	goalNode, err := pp.graph.GetNode(goalID)
	if err != nil {
		return nil, fmt.Errorf("goal node not found: %w", err)
	}

	pq := make(priorityQueue, 0)
	heap.Init(&pq)

	startPath := &pathNode{
		nodeID: startID,
		gCost:  0,
		fCost:  pp.heuristic(startNode, goalNode),
		parent: nil,
	}
	heap.Push(&pq, startPath)

	visited := make(map[int]float64)

	for pq.Len() > 0 {
		current := heap.Pop(&pq).(*pathNode)

		if current.nodeID == goalID {
			return reconstructPath(current), nil
		}

		if bestCost, exists := visited[current.nodeID]; exists && current.gCost > bestCost {
			continue
		}
		visited[current.nodeID] = current.gCost

		for _, edge := range pp.graph.GetNeighbors(current.nodeID) {
			neighborID := edge.ToNode
			neighborNode, err := pp.graph.GetNode(neighborID)
			if err != nil {
				continue
			}

			edgeWeight := edge.BaseWeight
			if customWeight, exists := weights[edge.ID]; exists {
				edgeWeight = customWeight
			}

			tentativeG := current.gCost + edgeWeight

			if bestCost, exists := visited[neighborID]; exists && tentativeG >= bestCost {
				continue
			}

			neighbor := &pathNode{
				nodeID:   neighborID,
				gCost:    tentativeG,
				fCost:    tentativeG + pp.heuristic(neighborNode, goalNode),
				parent:   current,
				edgeUsed: edge,
			}
			heap.Push(&pq, neighbor)
		}
	}

	return nil, fmt.Errorf("no path found from node %d to node %d", startID, goalID)
}
