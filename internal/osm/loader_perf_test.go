//go:build !race

// Perf-sensitive A* timing. Excluded under -race because the detector adds
// 5-10x overhead and inflates the measurement past the 50ms budget. Run via
// `go test ./internal/osm/...` (no -race) for the real number.
package osm

import (
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

func TestAStarP95Under50ms(t *testing.T) {
	g := loadCityOrSkip(t)
	planner := pathfinding.NewPathPlanner(g, nil) // the engine's default heuristic

	nodeIDs := make([]int, 0, g.NodeCount())
	for id := range g.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Ints(nodeIDs)

	rng := rand.New(rand.NewSource(1))
	const trials = 100
	durations := make([]time.Duration, 0, trials)

	attempts, success := 0, 0
	for success < trials && attempts < trials*10 {
		attempts++
		from := nodeIDs[rng.Intn(len(nodeIDs))]
		to := nodeIDs[rng.Intn(len(nodeIDs))]
		if from == to {
			continue
		}
		start := time.Now()
		_, err := planner.FindPath(from, to)
		elapsed := time.Since(start)
		if err != nil {
			continue
		}
		durations = append(durations, elapsed)
		success++
	}

	if success < trials {
		t.Fatalf("only %d/%d trials produced a path after %d attempts", success, trials, attempts)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50 := durations[len(durations)*50/100]
	p95 := durations[len(durations)*95/100]
	t.Logf("A* over %d random reachable pairs: p50=%v p95=%v max=%v", success, p50, p95, durations[len(durations)-1])

	if p95 > 50*time.Millisecond {
		t.Errorf("A* p95 must be under 50ms, got %v", p95)
	}
}

// The default heuristic gives up some route quality for speed. Compare its
// routes with the admissible heuristic's optimal ones on the same pairs.
func TestDefaultHeuristicRouteQuality(t *testing.T) {
	g := loadCityOrSkip(t)
	def := pathfinding.NewPathPlanner(g, nil)
	opt := pathfinding.NewPathPlanner(g, pathfinding.AdmissibleTimeHeuristic(g))
	meters := pathfinding.NewPathPlanner(g, graph.EuclideanDistance)

	nodeIDs := make([]int, 0, g.NodeCount())
	for id := range g.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Ints(nodeIDs)

	routeTime := func(route []*graph.Edge) float64 {
		var s float64
		for _, e := range route {
			s += e.BaseWeight
		}
		return s
	}
	p95 := func(ds []time.Duration) time.Duration {
		sorted := append([]time.Duration(nil), ds...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[len(sorted)*95/100]
	}

	rng := rand.New(rand.NewSource(7))
	var ratios, meterRatios []float64
	var defTimes, optTimes []time.Duration
	for len(ratios) < 100 {
		from, to := nodeIDs[rng.Intn(len(nodeIDs))], nodeIDs[rng.Intn(len(nodeIDs))]
		if from == to {
			continue
		}
		start := time.Now()
		best, err := opt.FindPath(from, to)
		optTimes = append(optTimes, time.Since(start))
		if err != nil {
			t.Fatalf("admissible %d->%d: %v", from, to, err)
		}
		start = time.Now()
		got, err := def.FindPath(from, to)
		defTimes = append(defTimes, time.Since(start))
		if err != nil {
			t.Fatalf("default %d->%d: %v", from, to, err)
		}
		ratios = append(ratios, routeTime(got)/routeTime(best))
		if m, err := meters.FindPath(from, to); err == nil {
			meterRatios = append(meterRatios, routeTime(m)/routeTime(best))
		}
	}

	sort.Float64s(ratios)
	var mean float64
	for _, r := range ratios {
		mean += r
	}
	mean /= float64(len(ratios))
	worst := ratios[len(ratios)-1]
	t.Logf("default route time vs optimal over %d pairs: mean %.3f, p95 %.3f, worst %.3f", len(ratios), mean, ratios[len(ratios)*95/100], worst)
	t.Logf("search p95: default %v, admissible %v", p95(defTimes).Round(time.Millisecond), p95(optTimes).Round(time.Millisecond))
	var meterMean float64
	for _, r := range meterRatios {
		meterMean += r
	}
	t.Logf("meters as the heuristic: routes average %.3f of optimal", meterMean/float64(len(meterRatios)))

	if worst > pathfinding.DefaultHeuristicWeight {
		t.Errorf("a route came out %.2fx optimal, past the %.0fx bound", worst, pathfinding.DefaultHeuristicWeight)
	}
	if mean > 1.25 {
		t.Errorf("routes average %.2fx optimal; want close to 1", mean)
	}
}
