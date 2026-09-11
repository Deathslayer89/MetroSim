// Package promexport defines MetroSim's Prometheus metrics. Trip metrics take
// their scenario and policy labels from each event's Meta, so the engine and
// metrics-aggregator share one SubscribeToBus.
package promexport

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

var (
	pickupWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metrosim_pickup_wait_seconds",
		Help:    "Seconds from ride request to passenger pickup.",
		Buckets: []float64{1, 5, 10, 20, 40, 60, 90, 120, 180, 300, 600},
	}, []string{"scenario", "policy"})

	tripDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metrosim_trip_duration_seconds",
		Help:    "Seconds from pickup to dropoff.",
		Buckets: []float64{30, 60, 120, 300, 600, 1200, 1800, 3600},
	}, []string{"scenario", "policy"})

	matched = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "metrosim_trips_matched_total",
		Help: "Total ride requests successfully assigned to a driver.",
	}, []string{"scenario", "policy"})

	// Fleet gauges are set by the engine each tick and registered only through
	// RegisterFleetGauges, so the aggregator doesn't serve them stuck at zero.
	idleDrivers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrosim_idle_drivers",
		Help: "Current count of idle drivers.",
	})

	activeTrips = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrosim_active_trips",
		Help: "Current count of trips in progress (assigned or picked-up).",
	})

	pendingRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrosim_pending_requests",
		Help: "Current count of unmatched ride requests.",
	})

	surgeFraction = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrosim_surge_fraction",
		Help: "Fraction of active surge cells with multiplier > 1.05.",
	})

	abandonedRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrosim_abandoned_requests",
		Help: "Requests that left the queue unmatched since the run started.",
	})

	fleetGaugesOnce sync.Once
)

// RegisterFleetGauges exposes the fleet gauges. Only a process that sets them
// should call it.
func RegisterFleetGauges() {
	fleetGaugesOnce.Do(func() {
		prometheus.MustRegister(idleDrivers, activeTrips, pendingRequests, surgeFraction, abandonedRequests)
	})
}

func SetAbandonedRequests(n int) { abandonedRequests.Set(float64(n)) }

func SetIdleDrivers(n int)     { idleDrivers.Set(float64(n)) }
func SetActiveTrips(n int)     { activeTrips.Set(float64(n)) }
func SetPendingRequests(n int) { pendingRequests.Set(float64(n)) }
func SetSurgeFraction(f float64) {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	surgeFraction.Set(f)
}

// SubscribeToBus feeds the trip metrics from the bus under the "promexport"
// consumer group.
func SubscribeToBus(bus events.Bus) error {
	err := events.SubscribeTripMatched(bus, "promexport", func(m *eventspb.TripMatched) {
		scenario, policy := labelsFromMeta(m.Meta)
		matched.WithLabelValues(scenario, policy).Inc()
	})
	if err != nil {
		return err
	}
	return events.SubscribeTripCompleted(bus, "promexport", func(c *eventspb.TripCompleted) {
		scenario, policy := labelsFromMeta(c.Meta)
		pickupWait.WithLabelValues(scenario, policy).Observe(c.PickupTime.AsTime().Sub(c.RequestTime.AsTime()).Seconds())
		tripDuration.WithLabelValues(scenario, policy).Observe(c.DropoffTime.AsTime().Sub(c.PickupTime.AsTime()).Seconds())
	})
}

func labelsFromMeta(m *eventspb.Meta) (scenario, policy string) {
	if m == nil {
		return "unknown", "unknown"
	}
	s, p := m.Scenario, m.Policy
	if s == "" {
		s = "unknown"
	}
	if p == "" {
		p = "unknown"
	}
	return s, p
}
