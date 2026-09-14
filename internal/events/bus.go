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

// Bus is the pub/sub contract. group is the Kafka consumer group, which
// MemoryBus ignores; an empty group joins none and reads every partition from
// the start. SubscribeBatched commits a batch only once onBatchEnd returns nil.
// SubscribeFromNow joins no group and starts at the newest record.
type Bus interface {
	Publish(topic string, msg proto.Message) error
	Subscribe(topic, group string, handler func(proto.Message)) error
	SubscribeBatched(topic, group string, onRecord func(proto.Message), onBatchEnd func() error) error
	SubscribeFromNow(topic string, handler func(proto.Message)) error
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

// SubscribeFromNow is Subscribe, since a MemoryBus keeps no history.
func (b *MemoryBus) SubscribeFromNow(topic string, handler func(proto.Message)) error {
	return b.SubscribeBatched(topic, "", handler, nil)
}
