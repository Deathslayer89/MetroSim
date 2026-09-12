package eta

import (
	"path/filepath"
	"testing"
)

// A model fit on a different feature layout is refused when it loads, not when
// its first prediction runs past the end of its weights.
func TestLoadRejectsOtherFeatureLayouts(t *testing.T) {
	m := &Model{
		Names:   []string{"dist_m", "surge", "hour"},
		Mean:    make([]float64, 3),
		Std:     []float64{1, 1, 1},
		Weights: make([]float64, 4),
	}
	path := filepath.Join(t.TempDir(), "model.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a model with three features")
	}
}
