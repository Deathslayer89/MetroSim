// Package kafka implements events.Bus on Kafka with franz-go. The producer is
// idempotent and keys each record by its Meta.PartitionKey. Publish doesn't wait
// for the broker; Close flushes what's still buffered.
package kafka

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// unmarshalFailures counts records that failed proto.Unmarshal. Those go to the
// DLQ, counted by dlqWrites.
var (
	unmarshalFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "metrosim_events_unmarshal_failures_total",
		Help: "Kafka records that failed proto.Unmarshal and were sent to the DLQ.",
	}, []string{"topic"})

	dlqWrites = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "metrosim_events_dlq_writes_total",
		Help: "Records written to the DLQ.",
	}, []string{"topic"})

	publishFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "metrosim_events_publish_failures_total",
		Help: "Records the producer gave up on; the event was not published.",
	}, []string{"topic"})
)

// A batch whose onBatchEnd fails is retried, backing off exponentially up to the
// cap, until it succeeds: a sink that can't write stalls its partitions rather
// than dropping trips. Vars so tests can shorten them.
var (
	endBatchBaseBackoff = 100 * time.Millisecond
	endBatchMaxBackoff  = 5 * time.Second
)

func dlqTopic(topic string) string { return "metrosim.dlq." + topic }

// Bus is the Kafka transport. Implements events.Bus over a single producer
// client; one consumer goroutine per (topic, group) subscription.
type Bus struct {
	seeds    []string
	producer *kgo.Client

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	consumers []*kgo.Client // for graceful Close
}

// New opens a producer client connected to seeds with idempotent writes and
// acks=all. Transactional writes are out of scope.
func New(seeds []string) (*Bus, error) {
	producer, err := kgo.NewClient(
		kgo.SeedBrokers(seeds...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(50*time.Millisecond),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Bus{seeds: seeds, producer: producer, ctx: ctx, cancel: cancel}, nil
}

// Close stops every consumer (waits for in-flight handlers to finish), flushes
// records the producer still holds, then closes the Kafka clients. Call before
// flushing any downstream writer that subscribed to this bus.
func (b *Bus) Close() {
	b.cancel()
	b.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := b.producer.Flush(ctx); err != nil {
		log.Printf("kafka: flush on close: %v", err)
	}
	cancel()
	b.producer.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.consumers {
		c.Close()
	}
}

// Publish hands msg to the producer, keyed by its Meta.PartitionKey, and
// returns without waiting for the broker, so records batch within the linger.
// The client retries failed sends; a record it gives up on is logged and
// counted. When its buffer is full, Publish blocks until there's room. A record
// with no key is left to the partitioner.
func (b *Bus) Publish(topic string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", topic, err)
	}
	rec := &kgo.Record{Topic: topic, Value: data}
	if k := partitionKey(msg); k != "" {
		rec.Key = []byte(k)
	}
	b.producer.Produce(context.Background(), rec, func(r *kgo.Record, err error) {
		if err != nil {
			publishFailures.WithLabelValues(r.Topic).Inc()
			log.Printf("kafka: produce %s failed: %v", r.Topic, err)
		}
	})
	return nil
}

// Subscribe runs handler on every record and commits once per fetch, so after a
// restart or rebalance a fetch can be delivered again: at-least-once.
func (b *Bus) Subscribe(topic, group string, handler func(proto.Message)) error {
	return b.SubscribeBatched(topic, group, handler, nil)
}

// SubscribeBatched calls onBatchEnd after each fetch has gone through onRecord
// and commits the fetch only once onBatchEnd succeeds, so a crash mid-batch
// means redelivery rather than loss. A failing onBatchEnd is retried. An empty
// group joins none: it reads every partition from the start and commits nothing.
func (b *Bus) SubscribeBatched(topic, group string, onRecord func(proto.Message), onBatchEnd func() error) error {
	factory, ok := decoderFor(topic)
	if !ok {
		return fmt.Errorf("no proto decoder registered for topic %q", topic)
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(b.seeds...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}
	if group != "" {
		opts = append(opts, kgo.ConsumerGroup(group), kgo.DisableAutoCommit())
	}
	consumer, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafka consumer for %s/%s: %w", topic, group, err)
	}

	b.mu.Lock()
	b.consumers = append(b.consumers, consumer)
	b.mu.Unlock()

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.consume(consumer, topic, group != "", factory, onRecord, onBatchEnd)
	}()
	return nil
}

func (b *Bus) consume(client *kgo.Client, topic string, commit bool, factory func() proto.Message, onRecord func(proto.Message), onBatchEnd func() error) {
	for {
		fetches := client.PollFetches(b.ctx)
		if b.ctx.Err() != nil {
			return
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			log.Printf("kafka: poll %s: %v", topic, errs[0].Err)
			if fetches.NumRecords() == 0 {
				if !b.sleep(time.Second) {
					return
				}
				continue
			}
		}

		var msgs []proto.Message
		var dead []*kgo.Record
		fetches.EachRecord(func(rec *kgo.Record) {
			msg := factory()
			if err := proto.Unmarshal(rec.Value, msg); err != nil {
				unmarshalFailures.WithLabelValues(topic).Inc()
				dead = append(dead, rec)
				return
			}
			msgs = append(msgs, msg)
		})
		if len(msgs) == 0 && len(dead) == 0 {
			continue
		}

		if len(msgs) > 0 && !b.deliverBatch(topic, msgs, onRecord, onBatchEnd) {
			return // closing mid-retry: leave the batch uncommitted
		}
		if !commit {
			continue // no group: nothing to move past, and a restart reads from the start
		}
		// A record that doesn't decode never will, so it goes to the DLQ. Keep at
		// it until the DLQ takes them, since the commit below moves past them.
		for len(dead) > 0 && !b.sendToDLQ(topic, dead) {
			if !b.sleep(endBatchMaxBackoff) {
				return
			}
		}
		if err := client.CommitUncommittedOffsets(context.Background()); err != nil {
			log.Printf("kafka: commit %s: %v", topic, err)
		}
	}
}

// deliverBatch runs msgs through onRecord and then onBatchEnd, retrying the
// whole batch with capped exponential backoff until it succeeds. It returns
// false only when the bus closes first.
func (b *Bus) deliverBatch(topic string, msgs []proto.Message, onRecord func(proto.Message), onBatchEnd func() error) bool {
	backoff := endBatchBaseBackoff
	for attempt := 1; ; attempt++ {
		for _, m := range msgs {
			onRecord(m)
		}
		if onBatchEnd == nil {
			return true
		}
		err := onBatchEnd()
		if err == nil {
			return true
		}
		log.Printf("kafka: %s batch of %d failed on attempt %d, retrying in %s: %v", topic, len(msgs), attempt, backoff, err)
		if !b.sleep(backoff) {
			return false
		}
		backoff = min(2*backoff, endBatchMaxBackoff)
	}
}

// sleep waits for d and reports false if the bus closed first.
func (b *Bus) sleep(d time.Duration) bool {
	select {
	case <-b.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// sendToDLQ copies batch to metrosim.dlq.<topic>, keys and values unchanged, and
// reports whether every record was acknowledged. A retry can write a record
// twice.
func (b *Bus) sendToDLQ(topic string, batch []*kgo.Record) bool {
	dst := dlqTopic(topic)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ok := true
	produced := 0
	for _, rec := range batch {
		dlqRec := &kgo.Record{Topic: dst, Key: rec.Key, Value: rec.Value}
		if err := b.producer.ProduceSync(ctx, dlqRec).FirstErr(); err != nil {
			ok = false
			continue
		}
		produced++
	}
	dlqWrites.WithLabelValues(topic).Add(float64(produced))
	return ok
}

// partitionKey returns the message's Meta.PartitionKey, or "" to let Kafka
// pick the partition.
func partitionKey(msg proto.Message) string {
	type withMeta interface {
		GetMeta() *eventspb.Meta
	}
	if m, ok := msg.(withMeta); ok && m.GetMeta() != nil {
		return m.GetMeta().GetPartitionKey()
	}
	return ""
}

// decoderFor maps a topic string to a constructor for an empty payload of the
// right proto type. Tied to the topic table in internal/events.
func decoderFor(topic string) (func() proto.Message, bool) {
	switch topic {
	case events.TopicTripRequested:
		return func() proto.Message { return &eventspb.TripRequested{} }, true
	case events.TopicTripMatched:
		return func() proto.Message { return &eventspb.TripMatched{} }, true
	case events.TopicTripPickedUp:
		return func() proto.Message { return &eventspb.TripPickedUp{} }, true
	case events.TopicTripCompleted:
		return func() proto.Message { return &eventspb.TripCompleted{} }, true
	case events.TopicTripAbandoned:
		return func() proto.Message { return &eventspb.TripAbandoned{} }, true
	case events.TopicTripCancelled:
		return func() proto.Message { return &eventspb.TripCancelled{} }, true
	case events.TopicDriverLocationUpdate:
		return func() proto.Message { return &eventspb.DriverLocationUpdate{} }, true
	case events.TopicSurgeUpdated:
		return func() proto.Message { return &eventspb.SurgeUpdated{} }, true
	default:
		return nil, false
	}
}
