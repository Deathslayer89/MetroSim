//go:build integration

// Runs against a broker at METROSIM_KAFKA_SEEDS (default localhost:9092) and
// skips when none is reachable. Start one with `make kafka-up`.
package kafka

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

func seedsFromEnv() []string {
	if v := os.Getenv("METROSIM_KAFKA_SEEDS"); v != "" {
		return strings.Split(v, ",")
	}
	return []string{"localhost:9092"}
}

func brokerReachable(seeds []string) bool {
	for _, s := range seeds {
		c, err := net.DialTimeout("tcp", s, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// A TripCompleted published on the bus comes back intact, partition key included.
func TestKafkaBusRoundTrip(t *testing.T) {
	seeds := seedsFromEnv()
	if !brokerReachable(seeds) {
		t.Skipf("no broker at %v; run make kafka-up or set METROSIM_KAFKA_SEEDS", seeds)
	}

	bus, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bus.Close()

	// A new group starts at the earliest offset, so records from earlier runs
	// arrive too. Wait for this run's ride ID.
	rideID := time.Now().UnixNano()
	got := make(chan *eventspb.TripCompleted, 1)
	group := fmt.Sprintf("test-roundtrip-%d", rideID)
	err = events.SubscribeTripCompleted(bus, group, func(m *eventspb.TripCompleted) {
		if m.RideId != rideID {
			return
		}
		select {
		case got <- m:
		default:
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	want := &eventspb.TripCompleted{
		Meta: &eventspb.Meta{
			EventId:      42,
			Scenario:     "test",
			Policy:       "batch",
			Seed:         7,
			PartitionKey: "trip:42",
		},
		RideId:    rideID,
		RequestId: 42,
		DriverId:  9,
	}
	if err := events.PublishTripCompleted(bus, want); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case m := <-got:
		if m.RequestId != want.RequestId || m.DriverId != want.DriverId {
			t.Errorf("payload mismatch: got %+v want %+v", m, want)
		}
		if m.GetMeta().GetPartitionKey() != "trip:42" {
			t.Errorf("partition key dropped: got %q", m.GetMeta().GetPartitionKey())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for round-trip delivery")
	}
}
