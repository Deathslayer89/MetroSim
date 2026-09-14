package main

import "testing"

func TestCheckFlags(t *testing.T) {
	for _, ok := range []struct {
		speed, etaWeight float64
		etaModel         string
	}{{1, 0, ""}, {10, 0.5, "models/eta.json"}} {
		if err := checkFlags(ok.speed, ok.etaWeight, ok.etaModel); err != nil {
			t.Errorf("checkFlags(%v, %v, %q): %v", ok.speed, ok.etaWeight, ok.etaModel, err)
		}
	}
	for _, bad := range []struct {
		speed, etaWeight float64
		etaModel         string
	}{{0, 0, ""}, {-1, 0, ""}, {1, 0.5, ""}} {
		if err := checkFlags(bad.speed, bad.etaWeight, bad.etaModel); err == nil {
			t.Errorf("checkFlags(%v, %v, %q) accepted it", bad.speed, bad.etaWeight, bad.etaModel)
		}
	}
}
