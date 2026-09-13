//go:build integration

// Runs against a broker at METROSIM_KAFKA_SEEDS (default localhost:9092) and
// skips when none is reachable. Start one with `make kafka-up`.
package kafka

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
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

// A member that joins while another is mid-batch mustn't be handed that
// batch's partitions until it's committed, or the batch reaches the group
// twice.
func TestKafkaBusRebalanceWaitsForTheBatchInFlight(t *testing.T) {
	seeds := seedsFromEnv()
	if !brokerReachable(seeds) {
		t.Skipf("no broker at %v; run make kafka-up or set METROSIM_KAFKA_SEEDS", seeds)
	}
	pub, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	marker := time.Now().UnixNano()
	const n = 40
	for i := int64(0); i < n; i++ {
		msg := &eventspb.TripCompleted{Meta: &eventspb.Meta{PartitionKey: fmt.Sprintf("trip:%d", marker+i)}, RideId: marker + i}
		if err := events.PublishTripCompleted(pub, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	pub.Close()

	var mu sync.Mutex
	deliveries := make(map[int64]int)
	onRecord := func(m *eventspb.TripCompleted) {
		if m.RideId >= marker && m.RideId < marker+n {
			mu.Lock()
			deliveries[m.RideId]++
			mu.Unlock()
		}
	}
	delivered := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(deliveries)
	}

	group := fmt.Sprintf("test-rebalance-%d", marker)
	first, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer first.Close()
	holding := make(chan struct{})
	var once sync.Once
	err = events.SubscribeTripCompletedBatched(first, group, onRecord, func() error {
		once.Do(func() {
			close(holding)
			time.Sleep(5 * time.Second) // the second member joins meanwhile
		})
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case <-holding:
	case <-time.After(30 * time.Second):
		t.Fatal("the first member got no batch")
	}

	second, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer second.Close()
	if err := events.SubscribeTripCompletedBatched(second, group, onRecord, func() error { return nil }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for delivered() < n && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	// A duplicate would come from the second member re-reading a partition, one
	// fetch wait after it's assigned.
	time.Sleep(2 * batchFetchWait)
	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != n {
		t.Fatalf("%d of %d records delivered", len(deliveries), n)
	}
	for id, count := range deliveries {
		if count > 1 {
			t.Errorf("record %d delivered %d times", id-marker, count)
		}
	}
}

// A batched subscriber writes a file per fetch, so records that trickle in
// over two seconds should arrive in one or two fetches, not one each.
func TestKafkaBusBatchedFetchesWaitToFill(t *testing.T) {
	seeds := seedsFromEnv()
	if !brokerReachable(seeds) {
		t.Skipf("no broker at %v; run make kafka-up or set METROSIM_KAFKA_SEEDS", seeds)
	}
	marker := time.Now().UnixNano()
	const n = 20
	var mu sync.Mutex
	seen, batches, inBatch := 0, 0, 0
	sub, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()
	err = events.SubscribeTripCompletedBatched(sub, fmt.Sprintf("test-fill-%d", marker), func(m *eventspb.TripCompleted) {
		if m.RideId >= marker && m.RideId < marker+n {
			mu.Lock()
			seen++
			inBatch++
			mu.Unlock()
		}
	}, func() error {
		mu.Lock()
		defer mu.Unlock()
		if inBatch > 0 {
			batches++
			inBatch = 0
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	pub, err := New(seeds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := int64(0); i < n; i++ {
		msg := &eventspb.TripCompleted{Meta: &eventspb.Meta{PartitionKey: fmt.Sprintf("trip:%d", marker+i)}, RideId: marker + i}
		if err := events.PublishTripCompleted(pub, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	pub.Close()

	deadline := time.Now().Add(3 * batchFetchWait)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := seen == n
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != n {
		t.Fatalf("%d of %d records arrived", seen, n)
	}
	if batches > 3 {
		t.Errorf("%d records arrived in %d fetches", n, batches)
	}
}
