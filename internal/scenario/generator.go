package scenario

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// Generator emits ride requests sampled from a Scenario's Poisson process.
// All randomness goes through a single seeded RNG so two runs with the same
// scenario yield byte-identical request sequences.
type Generator struct {
	scenario *Scenario
	rng      *rand.Rand
	pickup   *nodeSampler
	dropoff  *nodeSampler
	nextID   int
}

func NewGenerator(s *Scenario, g *graph.Graph) *Generator {
	pickup := buildNodeSampler(g, s.Arrivals.PickupHotspots)
	dropoff := buildNodeSampler(g, s.Arrivals.DropoffHotspots)
	return &Generator{
		scenario: s,
		rng:      rand.New(rand.NewSource(s.Seed)),
		pickup:   pickup,
		dropoff:  dropoff,
		nextID:   1,
	}
}

// VehicleSpawnNodes returns Count node IDs for initial vehicle placement:
// distinct uniform-random nodes, or draws from the pickup distribution when
// spawn is "pickups". Deterministic given the scenario seed.
func (g *Generator) VehicleSpawnNodes(gr *graph.Graph) []int {
	n := g.scenario.Vehicles.Count
	if g.scenario.Vehicles.Spawn == "pickups" {
		out := make([]int, 0, n)
		seen := make(map[int]bool, n)
		for tries := 0; len(out) < n && tries < 50*n; tries++ {
			if id := g.pickup.Sample(g.rng); !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		for len(out) < n {
			out = append(out, g.pickup.Sample(g.rng))
		}
		return out
	}

	ids := make([]int, 0, len(gr.Nodes))
	for id := range gr.Nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	g.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })

	if n > len(ids) {
		n = len(ids)
	}
	return ids[:n]
}

// RequestCount returns how many requests this generator has emitted so far.
func (g *Generator) RequestCount() int { return g.nextID - 1 }

// NextArrivals draws Poisson(rate(elapsed) * dt) requests for the next tick.
// Returns nil once elapsed exceeds the scenario duration so headless runners
// can drain without taking new arrivals.
func (g *Generator) NextArrivals(elapsed, dt time.Duration) []*dispatcher.Request {
	if elapsed > g.scenario.Duration {
		return nil
	}
	lambda := g.rateAt(elapsed)
	n := samplePoisson(lambda*dt.Seconds(), g.rng)
	if n == 0 {
		return nil
	}
	out := make([]*dispatcher.Request, 0, n)
	for i := 0; i < n; i++ {
		pu := g.pickup.Sample(g.rng)
		do := g.dropoff.Sample(g.rng)
		if pu == do {
			do = g.dropoff.Sample(g.rng)
		}
		out = append(out, &dispatcher.Request{
			ID:              g.nextID,
			PickupNode:      pu,
			DestinationNode: do,
		})
		g.nextID++
	}
	return out
}

func (g *Generator) rateAt(elapsed time.Duration) float64 {
	segs := g.scenario.Arrivals.RateSegments
	if len(segs) == 0 {
		return 0
	}
	t := elapsed.Seconds()
	if t <= segs[0].T.Seconds() {
		return segs[0].Rate
	}
	last := segs[len(segs)-1]
	if t >= last.T.Seconds() {
		return last.Rate
	}
	for i := 0; i < len(segs)-1; i++ {
		t0 := segs[i].T.Seconds()
		t1 := segs[i+1].T.Seconds()
		if t >= t0 && t <= t1 {
			frac := (t - t0) / (t1 - t0)
			return segs[i].Rate + frac*(segs[i+1].Rate-segs[i].Rate)
		}
	}
	return 0
}

// samplePoisson uses Knuth's algorithm, fine for the per-tick means here (well
// under 1) but slow once the mean passes about 20.
func samplePoisson(lambda float64, rng *rand.Rand) int {
	if lambda <= 0 {
		return 0
	}
	L := math.Exp(-lambda)
	k := 0
	p := 1.0
	for {
		k++
		p *= rng.Float64()
		if p <= L {
			return k - 1
		}
	}
}
