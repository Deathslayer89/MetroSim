package dispatcher

import (
	"math/rand"
	"sort"
	"testing"
)

func TestNearestKMatchesFullSort(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	idx := NewH3DriverIndex()
	var all []DriverLocation
	for id := 0; id < 2000; id++ {
		// A coarse grid makes distance ties common, so the ID tiebreak gets exercised.
		lat := 37.70 + float64(rng.Intn(40))*0.002
		lon := -122.50 + float64(rng.Intn(40))*0.002
		idx.Insert(id, lat, lon)
		all = append(all, DriverLocation{ID: id, Lat: lat, Lon: lon})
	}
	for q := 0; q < 50; q++ {
		lat, lon := 37.70+rng.Float64()*0.08, -122.50+rng.Float64()*0.08
		want := append([]DriverLocation(nil), all...)
		sort.Slice(want, func(i, j int) bool {
			di := squaredDistDeg(lat, lon, want[i].Lat, want[i].Lon)
			dj := squaredDistDeg(lat, lon, want[j].Lat, want[j].Lon)
			if di != dj {
				return di < dj
			}
			return want[i].ID < want[j].ID
		})
		got := idx.NearestK(lat, lon, 5)
		if len(got) != 5 {
			t.Fatalf("query %d: got %d drivers, want 5", q, len(got))
		}
		for i := range got {
			if got[i].ID != want[i].ID {
				t.Fatalf("query %d: rank %d is driver %d, want %d", q, i, got[i].ID, want[i].ID)
			}
		}
	}
}

// Drivers far outside the 8-ring disk force the full-fleet fallback on every query.
func BenchmarkNearestKFallback(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	idx := NewH3DriverIndex()
	for id := 0; id < 5000; id++ {
		idx.Insert(id, 37.0+rng.Float64(), -123.0+rng.Float64())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.NearestK(38.5, -121.5, 5)
	}
}
