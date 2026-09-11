package eta

import (
	"math"
	"path/filepath"
	"testing"
)

// Fit a model on data generated from a known linear rule and check it recovers
// the relationship: duration = 0.1*dist + 60*surge plus a small hourly term.
func TestFitRecoversLinearSignal(t *testing.T) {
	var X [][]float64
	var y []float64
	for dist := 200.0; dist <= 8000; dist += 200 {
		for surge := 1.0; surge <= 2.5; surge += 0.5 {
			for hour := 0.0; hour < 24; hour += 3 {
				f := Features(dist, surge, hour)
				target := 0.1*dist + 60*surge + 20*math.Sin(2*math.Pi*hour/24)
				X = append(X, f)
				y = append(y, target)
			}
		}
	}

	m, err := Fit(X, y, FeatureNames, 0.1)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if m.R2 < 0.95 {
		t.Errorf("R2 should be high on a near-linear signal, got %.3f", m.R2)
	}

	got := m.Predict(Features(4000, 1.5, 12))
	want := 0.1*4000 + 60*1.5 + 20*math.Sin(2*math.Pi*12/24)
	if math.Abs(got-want) > 0.15*want {
		t.Errorf("prediction off: got %.1f want ~%.1f", got, want)
	}
}

func TestPredictNeverNegative(t *testing.T) {
	m := &Model{
		Names:   FeatureNames,
		Mean:    []float64{1000, 1.5, 0, 0},
		Std:     []float64{500, 0.5, 1, 1},
		Weights: []float64{-1000, 0, 0, 0, 0}, // bias drives it negative
	}
	if got := m.Predict(Features(100, 1, 0)); got != 0 {
		t.Errorf("negative prediction should clamp to 0, got %.1f", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	m := &Model{
		Names:   FeatureNames,
		Mean:    []float64{1, 2, 3, 4},
		Std:     []float64{1, 1, 1, 1},
		Weights: []float64{10, 1, 2, 3, 4},
		TrainN:  100,
		RMSE:    12.5,
		R2:      0.88,
	}
	path := filepath.Join(t.TempDir(), "model.json")
	if err := m.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Predict(Features(5, 6, 7)) != m.Predict(Features(5, 6, 7)) {
		t.Error("round-trip changed predictions")
	}
}
