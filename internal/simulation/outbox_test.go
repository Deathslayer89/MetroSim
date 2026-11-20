package simulation

import (
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// recordingBus keeps the ride IDs it was handed, in order.
type recordingBus struct {
	events.Bus
	mu  sync.Mutex
	ids []int64
}

func (b *recordingBus) Publish(_ string, msg proto.Message) error {
	b.mu.Lock()
	b.ids = append(b.ids, msg.(*eventspb.TripCompleted).RideId)
	b.mu.Unlock()
	return nil
}

func TestOutboxFlushesInQueueOrderUnderConcurrentFlushes(t *testing.T) {
	bus := &recordingBus{}
	o := &outbox{Bus: bus}
	const writers, perWriter = 8, 500
	var next sync.Mutex
	id := int64(0)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				next.Lock()
				id++
				_ = o.Publish(events.TopicTripCompleted, &eventspb.TripCompleted{RideId: id})
				next.Unlock()
				o.flush()
			}
		}()
	}
	wg.Wait()
	o.flush()
	if len(bus.ids) != writers*perWriter {
		t.Fatalf("published %d events, want %d", len(bus.ids), writers*perWriter)
	}
	for i, got := range bus.ids {
		if got != int64(i+1) {
			t.Fatalf("event %d is ride %d, want %d", i, got, i+1)
		}
	}
}

// reentrantBus queues another event from inside Publish, as a MemoryBus subscriber calling back into the engine does.
type reentrantBus struct {
	recordingBus
	o *outbox
}

func (b *reentrantBus) Publish(topic string, msg proto.Message) error {
	_ = b.recordingBus.Publish(topic, msg)
	if id := msg.(*eventspb.TripCompleted).RideId; id < 3 {
		_ = b.o.Publish(topic, &eventspb.TripCompleted{RideId: id + 1})
		b.o.flush()
	}
	return nil
}

func TestOutboxFlushFromInsidePublishDoesNotDeadlock(t *testing.T) {
	bus := &reentrantBus{}
	o := &outbox{Bus: bus}
	bus.o = o
	_ = o.Publish(events.TopicTripCompleted, &eventspb.TripCompleted{RideId: 1})
	o.flush()
	if len(bus.ids) != 3 || bus.ids[0] != 1 || bus.ids[2] != 3 {
		t.Errorf("published %v, want [1 2 3]", bus.ids)
	}
}
