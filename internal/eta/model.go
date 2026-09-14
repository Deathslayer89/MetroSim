// Package eta fits and serves a ridge regression that predicts trip duration
// from straight-line distance, surge and hour of day.
package eta

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
)

// FeatureNames is the feature vector layout; Features() emits the same order.
var FeatureNames = []string{"dist_m", "surge", "hour_sin", "hour_cos"}

// Features turns raw inputs into the model's feature vector. hourOfDay is in
// [0,24); the sin/cos pair lets a linear model represent the daily cycle
// without a discontinuity at midnight.
func Features(distMeters, surge, hourOfDay float64) []float64 {
	rad := 2 * math.Pi * hourOfDay / 24
	return []float64{distMeters, surge, math.Sin(rad), math.Cos(rad)}
}

// Model is a standardized linear model: features are z-scored using Mean/Std
// captured at fit time, then dotted with Weights (Weights[0] is the bias).
type Model struct {
	Names   []string  `json:"feature_names"`
	Mean    []float64 `json:"mean"`
	Std     []float64 `json:"std"`
	Weights []float64 `json:"weights"` // len = len(Names)+1; [0] is bias
	TrainN  int       `json:"train_n"`
	RMSE    float64   `json:"train_rmse_s"`
	R2      float64   `json:"train_r2"`
}

func (m *Model) Predict(features []float64) float64 {
	y := m.Weights[0]
	for i, f := range features {
		z := 0.0
		if m.Std[i] != 0 {
			z = (f - m.Mean[i]) / m.Std[i]
		}
		y += m.Weights[i+1] * z
	}
	if y < 0 {
		return 0 // a negative travel time is meaningless
	}
	return y
}

func (m *Model) Save(path string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func Load(path string) (*Model, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Model
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	// Features() emits FeatureNames, so a model fit on another layout would
	// index past its weights on the first prediction.
	if !slices.Equal(m.Names, FeatureNames) {
		return nil, fmt.Errorf("model features %v, want %v", m.Names, FeatureNames)
	}
	if len(m.Weights) != len(m.Names)+1 {
		return nil, fmt.Errorf("model corrupt: %d weights for %d features", len(m.Weights), len(m.Names))
	}
	if len(m.Mean) != len(m.Names) || len(m.Std) != len(m.Names) {
		return nil, fmt.Errorf("model corrupt: mean/std lengths (%d/%d) != %d features", len(m.Mean), len(m.Std), len(m.Names))
	}
	return &m, nil
}

// Fit solves ridge regression w = (Z'Z + lambda*I)^-1 Z'y on standardized features Z.
// The bias is fit on the unstandardized mean of y (the intercept column is not
// regularized). Returns an error on too few rows or a singular system.
func Fit(X [][]float64, y []float64, names []string, lambda float64) (*Model, error) {
	n := len(X)
	if n < len(names)+2 {
		return nil, fmt.Errorf("need at least %d rows to fit %d features, got %d", len(names)+2, len(names), n)
	}
	d := len(names)

	mean := make([]float64, d)
	std := make([]float64, d)
	constant := make([]bool, d)
	for j := 0; j < d; j++ {
		for i := 0; i < n; i++ {
			mean[j] += X[i][j]
		}
		mean[j] /= float64(n)
	}
	for j := 0; j < d; j++ {
		var ss float64
		for i := 0; i < n; i++ {
			diff := X[i][j] - mean[j]
			ss += diff * diff
		}
		std[j] = math.Sqrt(ss / float64(n))
		if std[j] <= 1e-12*math.Max(1, math.Abs(mean[j])) {
			std[j] = 1 // never varies, so its standardized column is all zeros
			constant[j] = true
		}
	}

	// Standardized design matrix with an intercept column at index 0.
	z := make([][]float64, n)
	var yMean float64
	for _, v := range y {
		yMean += v
	}
	yMean /= float64(n)
	for i := 0; i < n; i++ {
		row := make([]float64, d+1)
		row[0] = 1
		for j := 0; j < d; j++ {
			row[j+1] = (X[i][j] - mean[j]) / std[j]
		}
		z[i] = row
	}

	// Normal equations: A = Z'Z + lambda*I, intercept unregularized; b = Z'y.
	p := d + 1
	A := make([][]float64, p)
	b := make([]float64, p)
	for i := range A {
		A[i] = make([]float64, p)
	}
	for i := 0; i < n; i++ {
		for a := 0; a < p; a++ {
			b[a] += z[i][a] * y[i]
			for c := 0; c < p; c++ {
				A[a][c] += z[i][a] * z[i][c]
			}
		}
	}
	for a := 1; a < p; a++ {
		A[a][a] += lambda
		if constant[a-1] {
			A[a][a]++ // holds its weight at 0 without leaving the system singular when lambda is 0
		}
	}

	w, err := solve(A, b)
	if err != nil {
		return nil, err
	}

	m := &Model{Names: names, Mean: mean, Std: std, Weights: w, TrainN: n}

	// Training-set fit quality.
	var sse, sst float64
	for i := 0; i < n; i++ {
		pred := m.Predict(X[i])
		sse += (y[i] - pred) * (y[i] - pred)
		sst += (y[i] - yMean) * (y[i] - yMean)
	}
	m.RMSE = math.Sqrt(sse / float64(n))
	if sst > 0 {
		m.R2 = 1 - sse/sst
	}
	return m, nil
}

// solve does Gaussian elimination with partial pivoting; the systems here are
// five by five.
func solve(A [][]float64, b []float64) ([]float64, error) {
	n := len(b)
	for col := 0; col < n; col++ {
		pivot := col
		for r := col + 1; r < n; r++ {
			if math.Abs(A[r][col]) > math.Abs(A[pivot][col]) {
				pivot = r
			}
		}
		if math.Abs(A[pivot][col]) < 1e-12 {
			return nil, fmt.Errorf("singular system at column %d (collinear features?)", col)
		}
		A[col], A[pivot] = A[pivot], A[col]
		b[col], b[pivot] = b[pivot], b[col]

		for r := col + 1; r < n; r++ {
			factor := A[r][col] / A[col][col]
			for c := col; c < n; c++ {
				A[r][c] -= factor * A[col][c]
			}
			b[r] -= factor * b[col]
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		sum := b[r]
		for c := r + 1; c < n; c++ {
			sum -= A[r][c] * x[c]
		}
		x[r] = sum / A[r][r]
	}
	return x, nil
}
