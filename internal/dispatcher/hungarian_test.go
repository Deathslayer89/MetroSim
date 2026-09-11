package dispatcher

import (
	"math"
	"math/rand"
	"testing"
)

func TestHungarianKnownOptimal(t *testing.T) {
	// Hand-checked: the optimal assignment is row 0 to column 1 and row 1 to
	// column 0, total cost 3.
	cost := [][]float64{
		{4, 1},
		{2, 9},
	}
	assign := hungarianMinCost(cost)
	got := cost[0][assign[0]] + cost[1][assign[1]]
	if math.Abs(got-3) > 1e-9 {
		t.Fatalf("want total cost 3 (1 + 2), got %v with %v", got, assign)
	}
}

func TestHungarianMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := 2 + rng.Intn(4)
		cost := make([][]float64, n)
		for i := range cost {
			cost[i] = make([]float64, n)
			for j := range cost[i] {
				cost[i][j] = rng.Float64() * 100
			}
		}
		assertValidOptimalAssignment(t, cost)
	}
}

func TestHungarianHandlesNegativeCosts(t *testing.T) {
	cost := [][]float64{
		{-5, -3, -1},
		{-2, -4, -6},
		{-7, -8, -2},
	}
	assertValidOptimalAssignment(t, cost)
}

func TestHungarianHandlesTies(t *testing.T) {
	// Every assignment is optimal; the solver must still return a valid permutation.
	n := 4
	cost := make([][]float64, n)
	for i := range cost {
		cost[i] = make([]float64, n)
		for j := range cost[i] {
			cost[i][j] = 7
		}
	}
	got := hungarianMinCost(cost)
	assertPermutation(t, got, n)
	total := 0.0
	for i, j := range got {
		total += cost[i][j]
	}
	if math.Abs(total-float64(n)*7) > 1e-9 {
		t.Errorf("all-equal tie matrix: want total %v, got %v", float64(n)*7, total)
	}
}

func TestHungarianIdentityOptimal(t *testing.T) {
	// cost[i][i] = 0 and 1 elsewhere, so the identity is optimal.
	n := 5
	cost := make([][]float64, n)
	for i := range cost {
		cost[i] = make([]float64, n)
		for j := range cost[i] {
			if i == j {
				cost[i][j] = 0
			} else {
				cost[i][j] = 1
			}
		}
	}
	got := hungarianMinCost(cost)
	for i := 0; i < n; i++ {
		if got[i] != i {
			t.Errorf("identity-optimal: row %d should map to %d, got %d", i, i, got[i])
		}
	}
}

func TestHungarianAntiDiagonalOptimal(t *testing.T) {
	// cost[i][n-1-i] = 0 and 1 elsewhere, so the reversal is optimal.
	n := 5
	cost := make([][]float64, n)
	for i := range cost {
		cost[i] = make([]float64, n)
		for j := range cost[i] {
			if j == n-1-i {
				cost[i][j] = 0
			} else {
				cost[i][j] = 1
			}
		}
	}
	got := hungarianMinCost(cost)
	for i := 0; i < n; i++ {
		if got[i] != n-1-i {
			t.Errorf("anti-diagonal: row %d should map to %d, got %d", i, n-1-i, got[i])
		}
	}
}

func TestHungarianRespectsLargeSentinel(t *testing.T) {
	// BatchPolicy pads forbidden (request, driver) pairs with 1e9. Hungarian
	// must skip those cells in favour of any finite real cost.
	cost := [][]float64{
		{5, 1e9},
		{1e9, 3},
	}
	got := hungarianMinCost(cost)
	if got[0] != 0 || got[1] != 1 {
		t.Errorf("sentinel-respecting: want (0->0, 1->1), got %v", got)
	}
}

func TestHungarianSingleton(t *testing.T) {
	got := hungarianMinCost([][]float64{{42}})
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("n=1: want [0], got %v", got)
	}
}

// assertValidOptimalAssignment checks both: (a) returned assignment is a valid
// permutation, (b) its total cost equals the brute-force minimum.
func assertValidOptimalAssignment(t *testing.T, cost [][]float64) {
	t.Helper()
	n := len(cost)
	got := hungarianMinCost(cost)
	assertPermutation(t, got, n)

	gotCost := 0.0
	for i, j := range got {
		gotCost += cost[i][j]
	}
	want := bruteForceMinCost(cost)
	if math.Abs(gotCost-want) > 1e-9 {
		t.Errorf("n=%d: Hungarian total=%v, brute-force min=%v, assign=%v",
			n, gotCost, want, got)
	}
}

func assertPermutation(t *testing.T, assign []int, n int) {
	t.Helper()
	if len(assign) != n {
		t.Fatalf("assignment length: want %d, got %d (assign=%v)", n, len(assign), assign)
	}
	seen := make(map[int]bool, n)
	for i, j := range assign {
		if j < 0 || j >= n {
			t.Errorf("row %d maps to out-of-range column %d", i, j)
		}
		if seen[j] {
			t.Errorf("column %d assigned twice in %v", j, assign)
		}
		seen[j] = true
	}
}

func bruteForceMinCost(cost [][]float64) float64 {
	n := len(cost)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	best := math.Inf(1)
	permute(perm, 0, func(p []int) {
		total := 0.0
		for i, j := range p {
			total += cost[i][j]
		}
		if total < best {
			best = total
		}
	})
	return best
}

func permute(a []int, k int, visit func([]int)) {
	if k == len(a)-1 {
		visit(a)
		return
	}
	for i := k; i < len(a); i++ {
		a[k], a[i] = a[i], a[k]
		permute(a, k+1, visit)
		a[k], a[i] = a[i], a[k]
	}
}
