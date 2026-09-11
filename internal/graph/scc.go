package graph

import "sort"

// LargestSCC returns the node IDs of the largest strongly connected component,
// the biggest set whose nodes can all reach each other. It runs Kosaraju's
// algorithm without recursion, so a deep graph can't overflow the stack.
func (g *Graph) LargestSCC() map[int]bool {
	// Pass 1: order nodes by DFS finish time on G.
	visited := make(map[int]bool, len(g.Nodes))
	order := make([]int, 0, len(g.Nodes))
	type frame struct {
		node int
		i    int
	}
	ids := make([]int, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids) // deterministic traversal
	for _, start := range ids {
		if visited[start] {
			continue
		}
		visited[start] = true
		stack := []frame{{start, 0}}
		for len(stack) > 0 {
			top := len(stack) - 1
			node := stack[top].node
			adj := g.Adjacency[node]
			if stack[top].i < len(adj) {
				next := adj[stack[top].i].ToNode
				stack[top].i++
				if !visited[next] {
					visited[next] = true
					stack = append(stack, frame{next, 0})
				}
			} else {
				order = append(order, node)
				stack = stack[:top]
			}
		}
	}

	// Reverse adjacency.
	radj := make(map[int][]int, len(g.Nodes))
	for _, e := range g.Edges {
		radj[e.ToNode] = append(radj[e.ToNode], e.FromNode)
	}

	// Pass 2: process in reverse finish order on the reverse graph; each DFS
	// tree is one SCC. Keep the largest.
	assigned := make(map[int]bool, len(g.Nodes))
	var largest map[int]bool
	for i := len(order) - 1; i >= 0; i-- {
		root := order[i]
		if assigned[root] {
			continue
		}
		comp := make(map[int]bool)
		assigned[root] = true
		comp[root] = true
		stack := []int{root}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, prev := range radj[n] {
				if !assigned[prev] {
					assigned[prev] = true
					comp[prev] = true
					stack = append(stack, prev)
				}
			}
		}
		if len(comp) > len(largest) {
			largest = comp
		}
	}
	return largest
}

// KeepLargestSCC prunes the graph in place to its largest strongly connected
// component and returns how many nodes it removed.
func (g *Graph) KeepLargestSCC() (removed int) {
	scc := g.LargestSCC()
	if len(scc) == 0 || len(scc) == len(g.Nodes) {
		return 0
	}
	removed = len(g.Nodes) - len(scc)

	for id := range g.Nodes {
		if !scc[id] {
			delete(g.Nodes, id)
		}
	}
	for id, e := range g.Edges {
		if !scc[e.FromNode] || !scc[e.ToNode] {
			delete(g.Edges, id)
		}
	}
	// Rebuild adjacency from surviving edges in sorted order (deterministic).
	edgeIDs := make([]int, 0, len(g.Edges))
	for id := range g.Edges {
		edgeIDs = append(edgeIDs, id)
	}
	sort.Ints(edgeIDs)
	g.Adjacency = make(map[int][]*Edge, len(g.Nodes))
	for id := range g.Nodes {
		g.Adjacency[id] = nil
	}
	for _, id := range edgeIDs {
		e := g.Edges[id]
		g.Adjacency[e.FromNode] = append(g.Adjacency[e.FromNode], e)
	}
	return removed
}
