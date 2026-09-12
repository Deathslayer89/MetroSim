package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

func shortBackoff(t *testing.T) {
	base, cap := endBatchBaseBackoff, endBatchMaxBackoff
	endBatchBaseBackoff, endBatchMaxBackoff = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { endBatchBaseBackoff, endBatchMaxBackoff = base, cap })
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
