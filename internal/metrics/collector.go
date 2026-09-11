package metrics

import (
	"sync"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/stats"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// SubscribeToBus records every TripCompleted under the "metrics" group.
func (mc *MetricsCollector) SubscribeToBus(bus events.Bus) {
	_ = events.SubscribeTripCompleted(bus, "metrics", func(c *eventspb.TripCompleted) {
		mc.RecordRide(&RideRecord{
			RideID:         int(c.RideId),
			RequestTime:    c.RequestTime.AsTime(),
			AssignmentTime: c.MatchTime.AsTime(),
			PickupTime:     c.PickupTime.AsTime(),
			DropoffTime:    c.DropoffTime.AsTime(),
		})
	})
}

type RideRecord struct {
	RideID         int
	RequestTime    time.Time
	AssignmentTime time.Time
	PickupTime     time.Time
	DropoffTime    time.Time
	WaitTime       float64 // seconds from request to pickup
	TripDuration   float64 // seconds from pickup to dropoff
}

// MetricsSummary covers completed trips only.
type MetricsSummary struct {
	TotalRides      int
	AvgWaitTime     float64
	P50WaitTime     float64
	P95WaitTime     float64
	AvgTripDuration float64
	Throughput      float64 // completed rides per simulated hour
}

type MetricsCollector struct {
	mu    sync.Mutex
	rides []*RideRecord
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

func (mc *MetricsCollector) RecordRide(record *RideRecord) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	record.WaitTime = record.PickupTime.Sub(record.RequestTime).Seconds()
	record.TripDuration = record.DropoffTime.Sub(record.PickupTime).Seconds()
	mc.rides = append(mc.rides, record)
}

func (mc *MetricsCollector) GetStats() MetricsSummary {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.rides) == 0 {
		return MetricsSummary{}
	}

	waits := make([]float64, len(mc.rides))
	var tripTotal float64
	first, last := mc.rides[0].RequestTime, mc.rides[0].DropoffTime
	for i, r := range mc.rides {
		waits[i] = r.WaitTime
		tripTotal += r.TripDuration
		if r.RequestTime.Before(first) {
			first = r.RequestTime
		}
		if r.DropoffTime.After(last) {
			last = r.DropoffTime
		}
	}

	s := MetricsSummary{
		TotalRides:      len(mc.rides),
		AvgWaitTime:     stats.Mean(waits),
		P50WaitTime:     stats.Percentile(waits, 0.5),
		P95WaitTime:     stats.Percentile(waits, 0.95),
		AvgTripDuration: tripTotal / float64(len(mc.rides)),
	}
	if hours := last.Sub(first).Hours(); hours > 0 {
		s.Throughput = float64(len(mc.rides)) / hours
	}
	return s
}

func (mc *MetricsCollector) GetRideCount() int {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return len(mc.rides)
}

// GetRides returns a copy of the slice; the records themselves are shared.
func (mc *MetricsCollector) GetRides() []*RideRecord {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return append([]*RideRecord(nil), mc.rides...)
}
