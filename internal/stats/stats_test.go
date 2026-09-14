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

// Over many samples of ten from a normal distribution, the 95% interval has to
// hold the true mean 95% of the time. An interval built on the normal quantile
// is too narrow at this size and holds it about 92% of the time.
func TestTInterval95CoverageAtTenSeeds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const trials, n = 4000, 10
	covered := 0
	xs := make([]float64, n)
	for i := 0; i < trials; i++ {
		for j := range xs {
			xs[j] = 5 + rng.NormFloat64()
		}
		if lo, hi := TInterval95(xs); lo <= 5 && 5 <= hi {
			covered++
		}
	}
	if rate := float64(covered) / trials; rate < 0.94 || rate > 0.96 {
		t.Errorf("95%% interval held the true mean in %.1f%% of %d samples", 100*rate, trials)
	}
}

func TestTInterval95KnownValues(t *testing.T) {
	// 1..10: mean 5.5, standard error sqrt(55/6)/sqrt(10), t(9) = 2.262157.
	lo, hi := TInterval95([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	half := 2.262157 * math.Sqrt(55.0/6) / math.Sqrt(10)
	if math.Abs(lo-(5.5-half)) > 1e-5 || math.Abs(hi-(5.5+half)) > 1e-5 {
		t.Errorf("want [%.4f, %.4f], got [%.4f, %.4f]", 5.5-half, 5.5+half, lo, hi)
	}
	if lo, hi := TInterval95([]float64{7}); lo != 7 || hi != 7 {
		t.Errorf("one value: want [7, 7], got [%v, %v]", lo, hi)
	}
}

func TestTQuantilePastTheTable(t *testing.T) {
	for df, want := range map[int]float64{31: 2.039513, 60: 2.000298, 120: 1.979930} {
		if got := tQuantile975(df); math.Abs(got-want) > 1e-4 {
			t.Errorf("t(%d) = %.6f, want %.6f", df, got, want)
		}
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

// 2^n overflows a float64 past about 1,000 trials, which made the p-value NaN.
func TestSignTestStaysFiniteForManyTrials(t *testing.T) {
	if p := SignTest(1000, 2000); p != 1 {
		t.Errorf("an even split of 2,000: want p = 1, got %v", p)
	}
	// 1,100 of 2,000 is about 4.5 standard deviations out.
	if p := SignTest(1100, 2000); !(p > 1e-6 && p < 1e-4) {
		t.Errorf("1,100 of 2,000: want p near 8e-6, got %v", p)
	}
}
