// Package stats holds the few sample statistics the experiment report uses.
package stats

import (
	"math"
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

// TInterval95 returns a 95% confidence interval for the mean of xs from
// Student's t with len(xs)-1 degrees of freedom. It needs two or more values.
func TInterval95(xs []float64) (lo, hi float64) {
	m := Mean(xs)
	n := len(xs)
	if n < 2 {
		return m, m
	}
	var ss float64
	for _, x := range xs {
		ss += (x - m) * (x - m)
	}
	half := tQuantile975(n-1) * math.Sqrt(ss/float64(n-1)/float64(n))
	return m - half, m + half
}

// t975 is the 97.5th percentile of Student's t for 1 to 30 degrees of freedom.
var t975 = [...]float64{
	12.706205, 4.302653, 3.182446, 2.776445, 2.570582, 2.446912, 2.364624, 2.306004, 2.262157, 2.228139,
	2.200985, 2.178813, 2.160369, 2.144787, 2.131450, 2.119905, 2.109816, 2.100922, 2.093024, 2.085963,
	2.079614, 2.073873, 2.068658, 2.063899, 2.059539, 2.055529, 2.051831, 2.048407, 2.045230, 2.042272,
}

// tQuantile975 reads t975 up to 30 degrees of freedom. Past that, a
// Cornish-Fisher expansion around the normal quantile is good to 1e-4.
func tQuantile975(df int) float64 {
	if df <= len(t975) {
		return t975[df-1]
	}
	const z = 1.959963984540054
	v := float64(df)
	z3, z5 := z*z*z, z*z*z*z*z
	return z + (z3+z)/(4*v) + (5*z5+16*z3+3*z)/(96*v*v)
}

// SignTest is the exact two-sided p-value for k wins in n paired trials at even
// odds; drop ties from n first. It sums in log space, since 2^n overflows past
// about 1,000 trials.
func SignTest(k, n int) float64 {
	if n == 0 {
		return 1
	}
	if k < n-k {
		k = n - k
	}
	lgn, _ := math.Lgamma(float64(n + 1))
	logTerm := func(i int) float64 { // log P(X = i) for X ~ Binomial(n, 1/2)
		a, _ := math.Lgamma(float64(i + 1))
		b, _ := math.Lgamma(float64(n - i + 1))
		return lgn - a - b - float64(n)*math.Ln2
	}
	top := logTerm(k) // the largest term of the upper tail
	var sum float64
	for i := k; i <= n; i++ {
		sum += math.Exp(logTerm(i) - top)
	}
	return math.Min(1, 2*math.Exp(top)*sum)
}
