package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

func shortBackoff(t *testing.T) {
	base, ceiling := endBatchBaseBackoff, endBatchMaxBackoff
	endBatchBaseBackoff, endBatchMaxBackoff = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { endBatchBaseBackoff, endBatchMaxBackoff = base, ceiling })
}

// A sink that fails for a while must get the batch once it recovers. Giving up
// and committing past the batch would lose its trips.
func TestDeliverBatchRetriesUntilTheSinkRecovers(t *testing.T) {
	shortBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &Bus{ctx: ctx, cancel: cancel}
	attempts := 0
	ok := b.deliverBatch("trip.completed", []proto.Message{&eventspb.TripCompleted{RideId: 1}}, func(proto.Message) {}, func() error {
		attempts++
		if attempts <= 10 {
			return errors.New("disk full")
		}
		return nil
	})
	if !ok || attempts != 11 {
		t.Errorf("want delivery on attempt 11, got ok=%v after %d attempts", ok, attempts)
	}
}

// Closing the bus mid-retry ends the loop without claiming success, so the
// batch stays uncommitted.
func TestDeliverBatchStopsWhenTheBusCloses(t *testing.T) {
	shortBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bus{ctx: ctx, cancel: cancel}
	attempts := 0
	ok := b.deliverBatch("trip.completed", []proto.Message{&eventspb.TripCompleted{}}, func(proto.Message) {}, func() error {
		attempts++
		if attempts == 3 {
			cancel()
		}
		return errors.New("disk full")
	})
	if ok {
		t.Error("deliverBatch reported success after the bus closed")
	}
}

// With no broker to take them, records pile up in the producer. Past
// maxBuffered, Publish has to drop events rather than block the tick that
// publishes them, and count what it drops.
func TestPublishDropsEventsWhenNoBrokerTakesThem(t *testing.T) {
	timeout := flushTimeout
	flushTimeout = 200 * time.Millisecond
	t.Cleanup(func() { flushTimeout = timeout })

	b, err := New([]string{"127.0.0.1:1"}) // nothing listens on port 1
	if err != nil {
		t.Fatal(err)
	}
	const sent = maxBuffered + 1000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < sent; i++ {
			_ = b.Publish(events.TopicDriverLocationUpdate, &eventspb.DriverLocationUpdate{DriverId: int64(i)})
		}
		b.Close()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Publish or Close blocked with no broker to take the records")
	}
	// The producer reports drops from its own goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for b.failing.Load() < sent-maxBuffered && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := b.failing.Load(); n < sent-maxBuffered {
		t.Errorf("%d events counted as unpublished, want at least the %d past the buffer", n, sent-maxBuffered)
	}
}

// stuckClient never finishes closing until released.
type stuckClient chan struct{}

func (c stuckClient) Close() { <-c }

// A client stuck closing, as a consumer leaving its group on an unreachable
// broker is, mustn't hold up the rest of shutdown.
func TestCloseAllGivesUpOnAStuckClient(t *testing.T) {
	stuck := make(stuckClient)
	defer close(stuck)
	result := make(chan bool, 1)
	go func() { result <- closeAll([]closer{stuck}, 50*time.Millisecond) }()
	select {
	case closed := <-result:
		if closed {
			t.Error("closeAll reported a stuck client as closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closeAll waited on a client that never closes")
	}
}
