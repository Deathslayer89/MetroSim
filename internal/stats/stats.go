// Package stats holds the few sample statistics the experiment report uses.
package stats

import (
	"math"
	"math/rand"
	"sort"
)

// Mean of a sample. Returns 0 on empty input.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// Percentile returns the p-th (0..1) percentile of xs via linear interpolation.
// Sorts a copy; xs is not modified.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	r := p * float64(len(sorted)-1)
	lo := int(r)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	w := r - float64(lo)
	return sorted[lo]*(1-w) + sorted[hi]*w
}

// BootstrapMeanCI returns a 95% percentile-bootstrap confidence interval for
// the mean of xs, from iters resamples.
func BootstrapMeanCI(xs []float64, iters int, rng *rand.Rand) (lo, hi float64) {
	if len(xs) == 0 || iters <= 0 {
		return 0, 0
	}
	means := make([]float64, iters)
	for i := range means {
		means[i] = sampleMean(xs, rng)
	}
	return Percentile(means, 0.025), Percentile(means, 0.975)
}

// SignTest is the exact two-sided p-value for k wins in n paired trials when
// each side is equally likely to win. Drop ties from n before calling.
func SignTest(k, n int) float64 {
	if n == 0 {
		return 1
	}
	if k < n-k {
		k = n - k
	}
	var tail float64
	for i := k; i <= n; i++ {
		tail += binomial(n, i)
	}
	return math.Min(1, 2*tail/math.Pow(2, float64(n)))
}

func binomial(n, k int) float64 {
	r := 1.0
	for i := 1; i <= k; i++ {
		r = r * float64(n-k+i) / float64(i)
	}
	return r
}

func sampleMean(xs []float64, rng *rand.Rand) float64 {
	var s float64
	for i := 0; i < len(xs); i++ {
		s += xs[rng.Intn(len(xs))]
	}
	return s / float64(len(xs))
}
