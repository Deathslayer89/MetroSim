// Package kafka implements events.Bus on Kafka with franz-go. The producer is
// idempotent and keys each record by its Meta.PartitionKey. Publish never waits
// for the broker; Close flushes what's still buffered.
package kafka

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
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
		Help: "Events never published: dropped while the producer was full, or given up on.",
	}, []string{"topic"})
)

// A failed batch is retried, backing off up to the cap, until it succeeds.
// Close waits flushTimeout for the flush and again for the clients. Vars so
// tests can shorten them.
var (
	endBatchBaseBackoff = 100 * time.Millisecond
	endBatchMaxBackoff  = 5 * time.Second
	flushTimeout        = 10 * time.Second
)

// maxBuffered is how many records the producer holds for the broker before
// Publish starts dropping events: about a minute of events from 180 cars.
const maxBuffered = 100_000

// A batched subscriber's fetch returns once it holds batchFetchBytes or has
// waited batchFetchWait.
const (
	batchFetchBytes = 1 << 20
	batchFetchWait  = 10 * time.Second
)

func dlqTopic(topic string) string { return "metrosim.dlq." + topic }

// Bus is the Kafka transport. Implements events.Bus over a single producer
// client; one consumer goroutine per (topic, group) subscription.
type Bus struct {
	seeds    []string
	producer *kgo.Client
	failing  atomic.Int64 // records dropped or failed since one was last delivered

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
		kgo.MaxBufferedRecords(maxBuffered),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Bus{seeds: seeds, producer: producer, ctx: ctx, cancel: cancel}, nil
}

// Close stops the consumers, flushes the producer and closes the clients,
// giving up on each step after flushTimeout. Call it before flushing a writer
// that subscribed to this bus.
func (b *Bus) Close() {
	b.cancel()
	b.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	if err := b.producer.Flush(ctx); err != nil {
		log.Printf("kafka: flush on close: %v; %d buffered events were not sent", err, b.producer.BufferedProduceRecords())
	}

	b.mu.Lock()
	clients := []closer{b.producer}
	for _, c := range b.consumers {
		clients = append(clients, c)
	}
	b.mu.Unlock()
	if !closeAll(clients, flushTimeout) {
		log.Printf("kafka: clients still closing after %s; not waiting for them", flushTimeout)
	}
}

type closer interface{ Close() }

// closeAll closes the clients concurrently and reports whether all of them
// finished within d; leaving a group on a dead broker can take minutes.
func closeAll(clients []closer, d time.Duration) bool {
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Publish hands msg to the producer, keyed by Meta.PartitionKey, and never
// waits for the broker. Once maxBuffered records are waiting, as in an outage,
// it drops the event rather than stall the simulation.
func (b *Bus) Publish(topic string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", topic, err)
	}
	rec := &kgo.Record{Topic: topic, Value: data}
	if k := partitionKey(msg); k != "" {
		rec.Key = []byte(k)
	}
	b.producer.TryProduce(context.Background(), rec, b.delivered)
	return nil
}

// delivered counts every failed record but logs only the first of a run and
// the delivery that ends it: two lines per outage, not one per event.
func (b *Bus) delivered(r *kgo.Record, err error) {
	if err == nil {
		if b.failing.Load() > 0 {
			if n := b.failing.Swap(0); n > 0 {
				log.Printf("kafka: delivering again after %d events were not published", n)
			}
		}
		return
	}
	publishFailures.WithLabelValues(r.Topic).Inc()
	if b.failing.Add(1) == 1 {
		log.Printf("kafka: produce %s failed, counting failures until a record gets through: %v", r.Topic, err)
	}
}

// Subscribe runs handler on every record and commits once per fetch, so after a
// restart or rebalance a fetch can be delivered again: at-least-once.
func (b *Bus) Subscribe(topic, group string, handler func(proto.Message)) error {
	return b.SubscribeBatched(topic, group, handler, nil)
}

// SubscribeBatched commits a fetch only after onBatchEnd succeeds, retrying
// the batch until it does, and holds rebalances until that commit. An empty
// group reads every partition from the start and commits nothing.
func (b *Bus) SubscribeBatched(topic, group string, onRecord func(proto.Message), onBatchEnd func() error) error {
	return b.subscribe(topic, group, kgo.NewOffset().AtStart(), onRecord, onBatchEnd)
}

// SubscribeFromNow reads each partition from its newest record on, in no group.
func (b *Bus) SubscribeFromNow(topic string, handler func(proto.Message)) error {
	return b.subscribe(topic, "", kgo.NewOffset().AtEnd(), handler, nil)
}

func (b *Bus) subscribe(topic, group string, start kgo.Offset, onRecord func(proto.Message), onBatchEnd func() error) error {
	factory, ok := decoderFor(topic)
	if !ok {
		return fmt.Errorf("no proto decoder registered for topic %q", topic)
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(b.seeds...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(start),
	}
	if group != "" {
		opts = append(opts, kgo.ConsumerGroup(group), kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll())
	}
	if onBatchEnd != nil {
		// A batched sink writes a file per fetch, so a fetch waits to fill up
		// instead of returning with the first record that arrives.
		opts = append(opts, kgo.FetchMinBytes(batchFetchBytes), kgo.FetchMaxWait(batchFetchWait))
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
	for b.consumeFetch(client, topic, commit, factory, onRecord, onBatchEnd) {
	}
}

// consumeFetch handles one poll and reports whether to keep going. A
// rebalance held back by the poll goes ahead when it returns.
func (b *Bus) consumeFetch(client *kgo.Client, topic string, commit bool, factory func() proto.Message, onRecord func(proto.Message), onBatchEnd func() error) bool {
	defer client.AllowRebalance()
	fetches := client.PollFetches(b.ctx)
	if b.ctx.Err() != nil {
		return false
	}
	if errs := fetches.Errors(); len(errs) > 0 {
		log.Printf("kafka: poll %s: %v", topic, errs[0].Err)
		if fetches.NumRecords() == 0 {
			return b.sleep(time.Second)
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
		return true
	}

	if len(msgs) > 0 && !b.deliverBatch(topic, msgs, onRecord, onBatchEnd) {
		return false // closing mid-retry: leave the batch uncommitted
	}
	if !commit {
		return true // no group: nothing to move past, and a restart reads from the start
	}
	// A record that doesn't decode never will, so it goes to the DLQ. Keep at
	// it until the DLQ takes them, since the commit below moves past them.
	for len(dead) > 0 && !b.sendToDLQ(topic, dead) {
		if !b.sleep(endBatchMaxBackoff) {
			return false
		}
	}
	// Closing abandons a commit still waiting on the broker; the batch comes
	// again after a restart.
	if err := client.CommitUncommittedOffsets(b.ctx); err != nil {
		log.Printf("kafka: commit %s: %v", topic, err)
	}
	return true
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
