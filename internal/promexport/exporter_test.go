package promexport

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

func read(t *testing.T, m any) *dto.Metric {
	t.Helper()
	var out dto.Metric
	if err := m.(prometheus.Metric).Write(&out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestTripMetricsTakeLabelsFromEachEvent(t *testing.T) {
	bus := events.NewMemoryBus()
	if err := SubscribeToBus(bus); err != nil {
		t.Fatal(err)
	}
	// A fresh scenario label per run, since -count reruns share the global metrics.
	scenario := fmt.Sprintf("promtest-%d", time.Now().UnixNano())
	meta := &eventspb.Meta{Scenario: scenario, Policy: "batch"}
	unknownBefore := read(t, matched.WithLabelValues("unknown", "unknown")).GetCounter().GetValue()
	_ = events.PublishTripMatched(bus, &eventspb.TripMatched{Meta: meta})
	_ = events.PublishTripMatched(bus, &eventspb.TripMatched{Meta: meta})
	_ = events.PublishTripMatched(bus, &eventspb.TripMatched{})
	if got := read(t, matched.WithLabelValues(scenario, "batch")).GetCounter().GetValue(); got != 2 {
		t.Errorf("matched{%s,batch} = %v, want 2", scenario, got)
	}
	if got := read(t, matched.WithLabelValues("unknown", "unknown")).GetCounter().GetValue(); got != unknownBefore+1 {
		t.Errorf("an event without Meta: matched{unknown,unknown} went %v to %v", unknownBefore, got)
	}

	start := time.Unix(1_700_000_000, 0)
	_ = events.PublishTripCompleted(bus, &eventspb.TripCompleted{
		Meta:        meta,
		RequestTime: timestamppb.New(start),
		PickupTime:  timestamppb.New(start.Add(45 * time.Second)),
		DropoffTime: timestamppb.New(start.Add(345 * time.Second)),
	})
	wait := read(t, pickupWait.WithLabelValues(scenario, "batch")).GetHistogram()
	if wait.GetSampleCount() != 1 || wait.GetSampleSum() != 45 {
		t.Errorf("pickup wait: %d samples summing to %v, want 1 of 45", wait.GetSampleCount(), wait.GetSampleSum())
	}
	for _, b := range wait.GetBucket() {
		if want := uint64(0); b.GetUpperBound() >= 60 {
			want = 1
			if b.GetCumulativeCount() != want {
				t.Errorf("bucket le=%v holds %d, want %d", b.GetUpperBound(), b.GetCumulativeCount(), want)
			}
		} else if b.GetCumulativeCount() != want {
			t.Errorf("bucket le=%v holds %d, want %d", b.GetUpperBound(), b.GetCumulativeCount(), want)
		}
	}
	if got := read(t, tripDuration.WithLabelValues(scenario, "batch")).GetHistogram().GetSampleSum(); got != 300 {
		t.Errorf("trip duration sum = %v, want 300", got)
	}
}

func TestSurgeFractionIsClampedToZeroOne(t *testing.T) {
	for in, want := range map[float64]float64{-0.5: 0, 0.25: 0.25, 1.7: 1} {
		SetSurgeFraction(in)
		if got := read(t, surgeFraction).GetGauge().GetValue(); got != want {
			t.Errorf("SetSurgeFraction(%v) left %v, want %v", in, got, want)
		}
	}
}

// fleetRegisteredByTest is set once registration happened, since -count reruns share the default registry.
var fleetRegisteredByTest bool

func TestFleetGaugesAreServedOnlyAfterRegistering(t *testing.T) {
	names := map[string]bool{
		"metrosim_idle_drivers": true, "metrosim_active_trips": true, "metrosim_pending_requests": true,
		"metrosim_surge_fraction": true, "metrosim_abandoned_requests": true,
	}
	served := func() int {
		families, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range families {
			if names[f.GetName()] {
				n++
			}
		}
		return n
	}
	if !fleetRegisteredByTest {
		if n := served(); n != 0 {
			t.Fatalf("%d fleet gauges served before RegisterFleetGauges", n)
		}
	}
	fleetRegisteredByTest = true
	RegisterFleetGauges()
	RegisterFleetGauges()
	SetIdleDrivers(7)
	if n := served(); n != len(names) {
		t.Errorf("%d fleet gauges served, want %d", n, len(names))
	}
	if got := read(t, idleDrivers).GetGauge().GetValue(); got != 7 {
		t.Errorf("idle drivers = %v, want 7", got)
	}
}
