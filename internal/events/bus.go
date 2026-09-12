// Package events is MetroSim's pub/sub layer. MemoryBus delivers in process;
// internal/events/kafka implements the same Bus on Kafka.
package events

import (
	"sync"

	"google.golang.org/protobuf/proto"
)

// Topic names are wire contracts: renaming one breaks every consumer.
const (
	TopicTripRequested        = "trip.requested"
	TopicTripMatched          = "trip.matched"
	TopicTripPickedUp         = "trip.picked_up"
	TopicTripCompleted        = "trip.completed"
	TopicTripAbandoned        = "trip.abandoned"
	TopicTripCancelled        = "trip.cancelled"
	TopicDriverLocationUpdate = "driver.location_update"
	TopicSurgeUpdated         = "surge.updated"
)

// Bus is the pub/sub contract. group is the Kafka consumer group; MemoryBus
// ignores it and delivers to every subscriber. An empty group joins none: the
// subscriber reads every partition from the start and commits nothing, which a
// view that keeps its state in memory needs to rebuild it after a restart.
//
// SubscribeBatched calls onBatchEnd after each fetched batch has gone through
// onRecord. Offsets commit only when it returns nil; an error means the batch
// is redelivered.
type Bus interface {
	Publish(topic string, msg proto.Message) error
	Subscribe(topic, group string, handler func(proto.Message)) error
	SubscribeBatched(topic, group string, onRecord func(proto.Message), onBatchEnd func() error) error
}

// RunInfo is constant for the lifetime of one simulation run. Publishers stamp
// it into Meta on every outgoing event.
type RunInfo struct {
	Scenario string
	Policy   string
	Seed     int64
}

// MemoryBus delivers synchronously on the publisher's goroutine, which keeps
// runs deterministic. For SubscribeBatched, every Publish is a batch of one.
type MemoryBus struct {
	mu   sync.RWMutex
	subs map[string][]memorySub
}

type memorySub struct {
	onRecord   func(proto.Message)
	onBatchEnd func() error // nil means no batching needed
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{subs: make(map[string][]memorySub)}
}

func (b *MemoryBus) Publish(topic string, msg proto.Message) error {
	b.mu.RLock()
	subs := b.subs[topic]
	b.mu.RUnlock()
	for _, s := range subs {
		s.onRecord(msg)
		if s.onBatchEnd != nil {
			_ = s.onBatchEnd() // memory bus has no offset to skip
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(topic, group string, handler func(proto.Message)) error {
	return b.SubscribeBatched(topic, group, handler, nil)
}

func (b *MemoryBus) SubscribeBatched(topic, group string, onRecord func(proto.Message), onBatchEnd func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], memorySub{onRecord: onRecord, onBatchEnd: onBatchEnd})
	return nil
}
