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

// A subscriber with no group reads each topic from the start and commits
// nothing, so one started later sees the same records again. live-view relies
// on this to rebuild its counts after a restart.
func TestKafkaBusGrouplessReadsFromTheStart(t *testing.T) {
	seeds := seedsFromEnv()
	if !brokerReachable(seeds) {
		t.Skipf("no broker at %v; run make kafka-up or set METROSIM_KAFKA_SEEDS", seeds)
	}
	pub, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pub.Close()
	marker := time.Now().UnixNano()
	for i := int64(0); i < 3; i++ {
		if err := events.PublishTripRequested(pub, &eventspb.TripRequested{RequestId: marker + i}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	for start := 1; start <= 2; start++ {
		bus, err := New(seeds)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		seen := make(chan int64, 16)
		err = events.SubscribeTripRequested(bus, "", func(m *eventspb.TripRequested) {
			if m.RequestId >= marker && m.RequestId < marker+3 {
				select {
				case seen <- m.RequestId:
				default:
				}
			}
		})
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		got := make(map[int64]bool)
		deadline := time.After(15 * time.Second)
		for len(got) < 3 {
			select {
			case id := <-seen:
				got[id] = true
			case <-deadline:
				bus.Close()
				t.Fatalf("start %d read %d of 3 records from the start", start, len(got))
			}
		}
		bus.Close()
	}
}

// Publish returns before the broker has the record, so Close has to flush:
// records published just before it must still arrive.
func TestKafkaBusCloseFlushesPublishedRecords(t *testing.T) {
	seeds := seedsFromEnv()
	if !brokerReachable(seeds) {
		t.Skipf("no broker at %v; run make kafka-up or set METROSIM_KAFKA_SEEDS", seeds)
	}
	pub, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	marker := time.Now().UnixNano()
	for i := int64(0); i < 5; i++ {
		if err := events.PublishTripRequested(pub, &eventspb.TripRequested{RequestId: marker + i}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub.Close()

	sub, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()
	seen := make(chan int64, 16)
	err = events.SubscribeTripRequested(sub, "", func(m *eventspb.TripRequested) {
		if m.RequestId >= marker && m.RequestId < marker+5 {
			select {
			case seen <- m.RequestId:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	got := make(map[int64]bool)
	deadline := time.After(15 * time.Second)
	for len(got) < 5 {
		select {
		case id := <-seen:
			got[id] = true
		case <-deadline:
			t.Fatalf("read back %d of the 5 records published before Close", len(got))
		}
	}
}
