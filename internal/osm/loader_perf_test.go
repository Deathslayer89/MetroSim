//go:build !race

// A* timing and route quality, excluded under -race, which slows search 5-10x.
package osm

import (
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

/**
 * BenchmarkFindPath times the default and admissible heuristics on the same 200
 * reachable pairs, after one warm-up pass, and reports p50 and p95 per search.
 * Run with `go test -run XXX -bench FindPath ./internal/osm`.
 */
func BenchmarkFindPath(b *testing.B) {
	g := loadCityOrSkip(b)
	nodeIDs := make([]int, 0, g.NodeCount())
	for id := range g.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Ints(nodeIDs)

	def := pathfinding.NewPathPlanner(g, nil)
	rng := rand.New(rand.NewSource(1))
	var pairs [][2]int
	for len(pairs) < 200 {
		from, to := nodeIDs[rng.Intn(len(nodeIDs))], nodeIDs[rng.Intn(len(nodeIDs))]
		if from == to {
			continue
		}
		if _, err := def.FindPath(from, to); err == nil {
			pairs = append(pairs, [2]int{from, to})
		}
	}

	for _, c := range []struct {
		name    string
		planner *pathfinding.PathPlanner
	}{
		{"weighted", def},
		{"admissible", pathfinding.NewPathPlanner(g, pathfinding.AdmissibleTimeHeuristic(g))},
	} {
		b.Run(c.name, func(b *testing.B) {
			for _, p := range pairs {
				_, _ = c.planner.FindPath(p[0], p[1])
			}
			durations := make([]time.Duration, 0, b.N)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := pairs[i%len(pairs)]
				start := time.Now()
				_, _ = c.planner.FindPath(p[0], p[1])
				durations = append(durations, time.Since(start))
			}
			b.StopTimer()
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			b.ReportMetric(float64(durations[len(durations)*50/100].Microseconds())/1000, "p50-ms")
			b.ReportMetric(float64(durations[len(durations)*95/100].Microseconds())/1000, "p95-ms")
		})
	}
}

// Asserts default-heuristic routes stay within 3x, and average within 1.25x, of the admissible optimum on 100 seeded pairs.
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
