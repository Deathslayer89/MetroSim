package stats

import (
	"math"
	"math/rand"
	"testing"
)

func TestPercentile(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := Percentile(xs, 0.5); math.Abs(got-5.5) > 1e-9 {
		t.Errorf("p50 of 1..10 = 5.5, got %v", got)
	}
	if got := Percentile(xs, 0.95); math.Abs(got-9.55) > 1e-9 {
		t.Errorf("p95 of 1..10 = 9.55, got %v", got)
	}
	if got := Percentile([]float64{42}, 0.95); got != 42 {
		t.Errorf("p95 of [42] = 42, got %v", got)
	}
	if got := Percentile(nil, 0.5); got != 0 {
		t.Errorf("p50 of nothing = 0, got %v", got)
	}
}

func TestMean(t *testing.T) {
	if got := Mean([]float64{1, 2, 3, 4}); math.Abs(got-2.5) > 1e-9 {
		t.Errorf("mean: want 2.5, got %v", got)
	}
}

// Across many samples from a known distribution, the 95% interval should hold
// the true mean about 95% of the time. One interval can't show that; a
// mis-indexed percentile or a 50% interval would show up here.
func TestBootstrapMeanCICoverage(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const trials, n = 400, 30
	covered := 0
	xs := make([]float64, n)
	for i := 0; i < trials; i++ {
		for j := range xs {
			xs[j] = 5 + rng.NormFloat64()
		}
		lo, hi := BootstrapMeanCI(xs, 1000, rng)
		if lo <= 5 && 5 <= hi {
			covered++
		}
	}
	if rate := float64(covered) / trials; rate < 0.90 || rate > 0.985 {
		t.Errorf("95%% interval held the true mean in %.1f%% of %d samples", 100*rate, trials)
	}
}

func TestBootstrapMeanCINarrowsWithMoreData(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	small := make([]float64, 10)
	large := make([]float64, 1000)
	for i := range small {
		small[i] = 5 + rng.NormFloat64()
	}
	for i := range large {
		large[i] = 5 + rng.NormFloat64()
	}

	loS, hiS := BootstrapMeanCI(small, 2000, rand.New(rand.NewSource(2)))
	loL, hiL := BootstrapMeanCI(large, 2000, rand.New(rand.NewSource(3)))
	if loL > 5 || hiL < 5 {
		t.Errorf("n=1000 interval [%.3f, %.3f] should cover the true mean 5", loL, hiL)
	}
	if hiL-loL >= hiS-loS {
		t.Errorf("interval should narrow with more data: n=10 width %.3f, n=1000 width %.3f", hiS-loS, hiL-loL)
	}
}

func TestSignTest(t *testing.T) {
	cases := []struct {
		k, n int
		want float64
	}{
		{10, 10, 2.0 / 1024},
		{0, 10, 2.0 / 1024},
		{9, 10, 22.0 / 1024},
		{5, 10, 1},
		{0, 0, 1},
	}
	for _, c := range cases {
		if got := SignTest(c.k, c.n); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("SignTest(%d, %d) = %v, want %v", c.k, c.n, got, c.want)
		}
	}
}
