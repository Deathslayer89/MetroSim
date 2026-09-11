package metrics

import (
	"math"
	"testing"
	"time"
)

// Feed known rides and pin every stat GetStats reports.
func TestGetStatsAggregates(t *testing.T) {
	mc := NewMetricsCollector()
	base := time.Unix(1_700_000_000, 0)
	for i, w := range []float64{10, 20, 30, 40, 50} {
		req := base.Add(time.Duration(i) * time.Minute)
		pickup := req.Add(time.Duration(w) * time.Second)
		mc.RecordRide(&RideRecord{RequestTime: req, PickupTime: pickup, DropoffTime: pickup.Add(100 * time.Second)})
	}

	s := mc.GetStats()
	eq := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if s.TotalRides != 5 {
		t.Errorf("TotalRides = %d, want 5", s.TotalRides)
	}
	eq("AvgWaitTime", s.AvgWaitTime, 30)
	eq("AvgTripDuration", s.AvgTripDuration, 100)
	eq("P50WaitTime", s.P50WaitTime, 30)
	eq("P95WaitTime", s.P95WaitTime, 48)
	// First request at 0 s, last dropoff at 4 min + 50 s + 100 s = 390 s.
	eq("Throughput", s.Throughput, 5/(390.0/3600))
}

func TestGetStatsEmpty(t *testing.T) {
	if s := NewMetricsCollector().GetStats(); s != (MetricsSummary{}) {
		t.Errorf("empty collector: want a zero summary, got %+v", s)
	}
}
