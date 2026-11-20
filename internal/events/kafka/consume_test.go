package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// fakeClient returns one canned poll and records commits and rebalance releases.
type fakeClient struct {
	fetches  kgo.Fetches
	commits  int
	released int
}

func (f *fakeClient) PollFetches(context.Context) kgo.Fetches { return f.fetches }
func (f *fakeClient) AllowRebalance()                         { f.released++ }
func (f *fakeClient) CommitUncommittedOffsets(context.Context) error {
	f.commits++
	return nil
}

func poll(topic string, pollErr error, values ...[]byte) kgo.Fetches {
	part := kgo.FetchPartition{Partition: 0, Err: pollErr}
	for i, v := range values {
		part.Records = append(part.Records, &kgo.Record{Topic: topic, Offset: int64(i), Value: v})
	}
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: topic, Partitions: []kgo.FetchPartition{part}}}}}
}

func completed(t *testing.T, id int64) []byte {
	t.Helper()
	data, err := proto.Marshal(&eventspb.TripCompleted{RideId: id})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testBus(t *testing.T) *Bus {
	t.Helper()
	shortBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Bus{ctx: ctx, cancel: cancel}
}

func fetchOnce(b *Bus, c *fakeClient, commit bool, onRecord func(proto.Message), onBatchEnd func() error) bool {
	factory, _ := decoderFor(events.TopicTripCompleted)
	return b.consumeFetch(c, events.TopicTripCompleted, commit, factory, onRecord, onBatchEnd)
}

func TestConsumeFetchCommitsOnlyAfterTheBatchEnds(t *testing.T) {
	b := testBus(t)
	c := &fakeClient{fetches: poll(events.TopicTripCompleted, nil, completed(t, 1), completed(t, 2))}
	var got []int64
	commitsAtEnd := -1
	more := fetchOnce(b, c, true, func(m proto.Message) {
		got = append(got, m.(*eventspb.TripCompleted).RideId)
	}, func() error {
		commitsAtEnd = c.commits
		return nil
	})
	if !more || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("delivered %v (continue %v), want rides 1 and 2", got, more)
	}
	if commitsAtEnd != 0 || c.commits != 1 || c.released != 1 {
		t.Errorf("commits before batch end %d, after %d, rebalance released %d; want 0, 1, 1", commitsAtEnd, c.commits, c.released)
	}
}

func TestConsumeFetchSendsUndecodableRecordsToTheDLQBeforeCommitting(t *testing.T) {
	b := testBus(t)
	c := &fakeClient{fetches: poll(events.TopicTripCompleted, nil, completed(t, 1), []byte{0xff, 0xff})}
	var dead []*kgo.Record
	attempts := 0
	b.dlq = func(topic string, recs []*kgo.Record) bool {
		attempts++
		if attempts == 1 {
			return false
		}
		if c.commits != 0 {
			t.Error("committed before the DLQ took the bad record")
		}
		dead = recs
		return true
	}
	delivered := 0
	fetchOnce(b, c, true, func(proto.Message) { delivered++ }, func() error { return nil })
	if delivered != 1 || len(dead) != 1 || dead[0].Offset != 1 || c.commits != 1 {
		t.Errorf("delivered %d, dead-lettered %d, commits %d; want 1, 1 (offset 1), 1", delivered, len(dead), c.commits)
	}
}

func TestConsumeFetchLeavesAFailingBatchUncommittedWhenTheBusCloses(t *testing.T) {
	b := testBus(t)
	c := &fakeClient{fetches: poll(events.TopicTripCompleted, nil, completed(t, 1))}
	tries := 0
	more := fetchOnce(b, c, true, func(proto.Message) {}, func() error {
		if tries++; tries == 3 {
			b.cancel()
		}
		return errors.New("disk full")
	})
	if more || c.commits != 0 {
		t.Errorf("continue %v, commits %d after the bus closed mid-retry; want false, 0", more, c.commits)
	}
}

// A poll can return an error alongside records; the records still count.
func TestConsumeFetchDeliversRecordsThatCameWithAPollError(t *testing.T) {
	b := testBus(t)
	c := &fakeClient{fetches: poll(events.TopicTripCompleted, errors.New("broker went away"), completed(t, 5))}
	var got int64
	fetchOnce(b, c, true, func(m proto.Message) { got = m.(*eventspb.TripCompleted).RideId }, nil)
	if got != 5 || c.commits != 1 {
		t.Errorf("delivered ride %d with %d commits; want ride 5, 1 commit", got, c.commits)
	}
}

func TestConsumeFetchWithoutAGroupNeverCommits(t *testing.T) {
	b := testBus(t)
	c := &fakeClient{fetches: poll(events.TopicTripCompleted, nil, completed(t, 1))}
	fetchOnce(b, c, false, func(proto.Message) {}, nil)
	if c.commits != 0 {
		t.Errorf("committed %d times with no group", c.commits)
	}
}

func TestEveryTopicHasADecoder(t *testing.T) {
	for _, topic := range []string{
		events.TopicTripRequested, events.TopicTripMatched, events.TopicTripPickedUp, events.TopicTripCompleted,
		events.TopicTripAbandoned, events.TopicTripCancelled, events.TopicDriverLocationUpdate, events.TopicSurgeUpdated,
	} {
		factory, ok := decoderFor(topic)
		if !ok || factory() == nil {
			t.Errorf("no decoder for %s", topic)
		}
	}
	if _, ok := decoderFor("no.such.topic"); ok {
		t.Error("an unknown topic got a decoder")
	}
	if err := (&Bus{}).Subscribe("no.such.topic", "g", func(proto.Message) {}); err == nil {
		t.Error("subscribing to an unknown topic succeeded")
	}
	if got := dlqTopic(events.TopicTripCompleted); got != "metrosim.dlq.trip.completed" {
		t.Errorf("dlq topic %q", got)
	}
}
