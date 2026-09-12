package scenario

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

func buildTinyGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadGraphFromCSV(
		"../../data/graphs/test_nodes.csv",
		"../../data/graphs/test_edges.csv",
	)
	if err != nil {
		t.Fatalf("load test graph: %v", err)
	}
	return g
}

func mustGenerator(t *testing.T, s *Scenario, g *graph.Graph) *Generator {
	t.Helper()
	gen, err := NewGenerator(s, g)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	return gen
}

func mkScenario(seed int64) *Scenario {
	return &Scenario{
		Name:     "test",
		Duration: 60 * time.Second,
		Seed:     seed,
		Vehicles: VehicleConfig{Count: 5, Spawn: "random"},
		Arrivals: ArrivalConfig{
			RateSegments: []RatePoint{
				{T: 0, Rate: 2.0},
				{T: 60 * time.Second, Rate: 2.0},
			},
		},
	}
}

// Two generators with the same seed must produce identical request streams.
func TestGeneratorDeterministic(t *testing.T) {
	g := buildTinyGraph(t)
	s := mkScenario(123)

	collect := func() (spawn []int, reqs [][3]int) {
		gen := mustGenerator(t, s, g)
		spawn = gen.VehicleSpawnNodes(g)
		const dt = 100 * time.Millisecond
		for tick := 0; tick < 600; tick++ {
			elapsed := time.Duration(tick) * dt
			for _, r := range gen.NextArrivals(elapsed, dt) {
				reqs = append(reqs, [3]int{r.ID, r.PickupNode, r.DestinationNode})
			}
		}
		return
	}

	spawnA, reqsA := collect()
	spawnB, reqsB := collect()

	if !equalInts(spawnA, spawnB) {
		t.Errorf("spawn nodes differ: A=%v B=%v", spawnA, spawnB)
	}
	if len(reqsA) != len(reqsB) {
		t.Fatalf("arrival counts differ: A=%d B=%d", len(reqsA), len(reqsB))
	}
	for i := range reqsA {
		if reqsA[i] != reqsB[i] {
			t.Errorf("request %d differs: A=%v B=%v", i, reqsA[i], reqsB[i])
		}
	}
}

// Different seeds must produce different request streams.
func TestGeneratorDifferentSeedsDiffer(t *testing.T) {
	g := buildTinyGraph(t)

	collect := func(seed int64) [][3]int {
		gen := mustGenerator(t, mkScenario(seed), g)
		var out [][3]int
		const dt = 100 * time.Millisecond
		for tick := 0; tick < 600; tick++ {
			for _, r := range gen.NextArrivals(time.Duration(tick)*dt, dt) {
				out = append(out, [3]int{r.ID, r.PickupNode, r.DestinationNode})
			}
		}
		return out
	}

	a := collect(1)
	b := collect(2)
	if len(a) == 0 {
		t.Fatal("no arrivals at all")
	}
	identical := len(a) == len(b)
	if identical {
		for i := range a {
			if a[i] != b[i] {
				identical = false
				break
			}
		}
	}
	if identical {
		t.Errorf("seeds 1 and 2 produced identical streams, so the seed isn't used")
	}
}

// Rate interpolation must produce the expected piecewise-linear values.
func TestRateInterpolation(t *testing.T) {
	s := &Scenario{
		Arrivals: ArrivalConfig{RateSegments: []RatePoint{
			{T: 0, Rate: 0},
			{T: 10 * time.Second, Rate: 10},
		}},
	}
	g := &Generator{scenario: s}
	cases := []struct {
		at   time.Duration
		want float64
	}{
		{0, 0},
		{5 * time.Second, 5},
		{10 * time.Second, 10},
		{99 * time.Second, 10}, // beyond last segment
	}
	for _, c := range cases {
		got := g.rateAt(c.at)
		if got != c.want {
			t.Errorf("rateAt(%v) = %v, want %v", c.at, got, c.want)
		}
	}
}

// samplePoisson's mean and variance must both be close to lambda.
func TestSamplePoissonMoments(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	const lambda = 2.5
	const n = 200000
	sum, sumSq := 0, 0
	for i := 0; i < n; i++ {
		k := samplePoisson(lambda, rng)
		sum += k
		sumSq += k * k
	}
	mean := float64(sum) / n
	variance := float64(sumSq)/n - mean*mean
	if math.Abs(mean-lambda) > 0.05 {
		t.Errorf("sample mean %.4f, want about %.2f", mean, lambda)
	}
	if math.Abs(variance-lambda) > 0.10 {
		t.Errorf("sample variance %.4f, want about %.2f", variance, lambda)
	}
}

// Total arrivals must track rate*duration; averaged over seeds so a wrong rate
// (a dropped dt factor, say) lands well outside the band.
func TestGeneratorArrivalRateTracksLambda(t *testing.T) {
	g := buildTinyGraph(t)
	const dt = 100 * time.Millisecond
	const seeds = 40
	const want = 2.0 * 60.0 // mkScenario: 2 requests/s for 60 s
	total := 0
	for seed := int64(0); seed < seeds; seed++ {
		gen := mustGenerator(t, mkScenario(seed), g)
		for tick := 0; tick < 600; tick++ {
			total += len(gen.NextArrivals(time.Duration(tick)*dt, dt))
		}
	}
	got := float64(total) / seeds
	if math.Abs(got-want)/want > 0.10 {
		t.Errorf("mean arrivals per run = %.1f, want about %.0f", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
