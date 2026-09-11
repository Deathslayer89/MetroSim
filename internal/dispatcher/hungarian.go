package dispatcher

import "math"

// hungarianMinCost solves the assignment problem on a square cost matrix using
// Kuhn-Munkres in O(n^3). Returns assignment[i] = j meaning row i is matched
// to column j. The input matrix is not modified.
//
// Inputs are expected to be square; the caller pads rectangular matrices with
// a large constant (see matchBatch). It doesn't handle Inf or NaN; the caller
// uses a large finite cost for unreachable pairs.
func hungarianMinCost(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}

	// 1-indexed internals are easier to read against the textbook KM algorithm.
	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1) // p[j] = row matched to column j
	way := make([]int, n+1)

	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for k := range minv {
			minv[k] = math.Inf(1)
		}

		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}

	assign := make([]int, n)
	for j := 1; j <= n; j++ {
		if p[j] > 0 {
			assign[p[j]-1] = j - 1
		}
	}
	return assign
}
