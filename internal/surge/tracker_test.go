package surge

import (
	"math"
	"testing"
	"time"

	"github.com/uber/h3-go/v4"
)

func cellAt(lat, lon float64) h3.Cell {
	c, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lon}, Resolution)
	if err != nil {
		panic(err)
	}
	return c
}

func TestTrackerBaselineIsOne(t *testing.T) {
	tr := NewTracker()
	if got := tr.MultiplierAt(37.77, -122.42); got != 1.0 {
		t.Errorf("baseline (no data): want 1.0, got %v", got)
	}
}

// A sustained 10:1 imbalance holds surge at the 3.0 cap.
func TestTrackerRespondsToImbalance(t *testing.T) {
	tr := NewTracker()
	c := cellAt(37.77, -122.42)

	now := time.Unix(0, 0)
	requests := map[h3.Cell]int{c: 10}
	drivers := map[h3.Cell]int{c: 1} // raw = 10, clamped to 3

	tr.Update(now, requests, drivers) // first update: alpha=1, full raw
	if got := tr.Multiplier(c); math.Abs(got-3.0) > 1e-6 {
		t.Fatalf("after first spike update: want clamp to 3.0, got %v", got)
	}

	// 30 more seconds of the same imbalance.
	for i := 0; i < 30; i++ {
		now = now.Add(time.Second)
		tr.Update(now, requests, drivers)
	}
	if got := tr.Multiplier(c); math.Abs(got-3.0) > 1e-6 {
		t.Errorf("after sustained imbalance: want 3.0, got %v", got)
	}
}

// With demand gone, surge decays to baseline; ten EMATau periods is enough to
// fall below the prune threshold.
func TestTrackerDecaysToBaseline(t *testing.T) {
	tr := NewTracker()
	c := cellAt(37.77, -122.42)

	now := time.Unix(0, 0)
	tr.Update(now, map[h3.Cell]int{c: 10}, map[h3.Cell]int{c: 1})

	steps := int(10 * EMATau.Seconds())
	for i := 0; i < steps; i++ {
		now = now.Add(time.Second)
		tr.Update(now, nil, nil)
	}
	if got := tr.Multiplier(c); math.Abs(got-1.0) > 0.005 {
		t.Errorf("after ten tau of zero demand: want ~1.0, got %v", got)
	}
	if _, ok := tr.Snapshot()[c]; ok {
		t.Errorf("fully-decayed cell should be pruned from snapshot")
	}
}

// One short pulse of demand followed by none should leave surge between the
// baseline and the cap.
func TestTrackerSmoothsTransients(t *testing.T) {
	tr := NewTracker()
	c := cellAt(37.77, -122.42)

	now := time.Unix(0, 0)
	tr.Update(now, map[h3.Cell]int{c: 10}, map[h3.Cell]int{c: 1}) // first update = full
	now = now.Add(100 * time.Millisecond)
	tr.Update(now, nil, nil) // tiny dt, drift back toward baseline
	got := tr.Multiplier(c)
	if got >= 3.0 || got <= 1.0 {
		t.Errorf("after smoothing pulse: want 1.0 < surge < 3.0, got %v", got)
	}
}

func TestBucketLatLons(t *testing.T) {
	pts := []LatLon{
		{Lat: 37.77, Lon: -122.42},
		{Lat: 37.77, Lon: -122.42}, // same cell
		{Lat: 40.71, Lon: -74.00},  // NYC, a different cell
	}
	buckets := BucketLatLons(pts, Resolution)
	if len(buckets) != 2 {
		t.Fatalf("want 2 distinct cells, got %d", len(buckets))
	}
	var maxCount int
	for _, c := range buckets {
		if c > maxCount {
			maxCount = c
		}
	}
	if maxCount != 2 {
		t.Errorf("want max cell count 2, got %d", maxCount)
	}
}
