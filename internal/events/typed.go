package events

import (
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// Stamper turns a fresh proto.Message into one carrying the next event_id and
// the current run metadata. Engine holds the only Stamper instance, so IDs
// count up across all its events and start again at 1 with every run.
type Stamper struct {
	mu      sync.RWMutex
	info    RunInfo
	counter atomic.Uint64
}

func NewStamper(info RunInfo) *Stamper {
	return &Stamper{info: info}
}

// SetRunInfo replaces the run metadata stamped on subsequent events. Use at
// startup once scenario+policy+seed are known.
func (s *Stamper) SetRunInfo(info RunInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info = info
}

// MetaFor returns a freshly-numbered Meta stamped at sim time t.
func (s *Stamper) MetaFor(t time.Time) *eventspb.Meta {
	s.mu.RLock()
	info := s.info
	s.mu.RUnlock()
	return &eventspb.Meta{
		EventId:  s.counter.Add(1),
		Time:     timestamppb.New(t),
		Scenario: info.Scenario,
		Policy:   info.Policy,
		Seed:     info.Seed,
	}
}

// Typed helpers, one pair per topic, so the topic and payload type are checked
// together at compile time.

func PublishTripRequested(bus Bus, msg *eventspb.TripRequested) error {
	return bus.Publish(TopicTripRequested, msg)
}
func SubscribeTripRequested(bus Bus, group string, fn func(*eventspb.TripRequested)) error {
	return bus.Subscribe(TopicTripRequested, group, func(m proto.Message) {
		fn(m.(*eventspb.TripRequested))
	})
}

func PublishTripMatched(bus Bus, msg *eventspb.TripMatched) error {
	return bus.Publish(TopicTripMatched, msg)
}
func SubscribeTripMatched(bus Bus, group string, fn func(*eventspb.TripMatched)) error {
	return bus.Subscribe(TopicTripMatched, group, func(m proto.Message) {
		fn(m.(*eventspb.TripMatched))
	})
}

func PublishTripPickedUp(bus Bus, msg *eventspb.TripPickedUp) error {
	return bus.Publish(TopicTripPickedUp, msg)
}
func SubscribeTripPickedUp(bus Bus, group string, fn func(*eventspb.TripPickedUp)) error {
	return bus.Subscribe(TopicTripPickedUp, group, func(m proto.Message) {
		fn(m.(*eventspb.TripPickedUp))
	})
}

func PublishTripCompleted(bus Bus, msg *eventspb.TripCompleted) error {
	return bus.Publish(TopicTripCompleted, msg)
}
func SubscribeTripCompleted(bus Bus, group string, fn func(*eventspb.TripCompleted)) error {
	return bus.Subscribe(TopicTripCompleted, group, func(m proto.Message) {
		fn(m.(*eventspb.TripCompleted))
	})
}

// SubscribeTripCompletedBatched is the batched form; see Bus.SubscribeBatched.
func SubscribeTripCompletedBatched(bus Bus, group string, onRecord func(*eventspb.TripCompleted), onBatchEnd func() error) error {
	return bus.SubscribeBatched(TopicTripCompleted, group, func(m proto.Message) {
		onRecord(m.(*eventspb.TripCompleted))
	}, onBatchEnd)
}

func PublishTripAbandoned(bus Bus, msg *eventspb.TripAbandoned) error {
	return bus.Publish(TopicTripAbandoned, msg)
}
func SubscribeTripAbandoned(bus Bus, group string, fn func(*eventspb.TripAbandoned)) error {
	return bus.Subscribe(TopicTripAbandoned, group, func(m proto.Message) {
		fn(m.(*eventspb.TripAbandoned))
	})
}

func PublishTripCancelled(bus Bus, msg *eventspb.TripCancelled) error {
	return bus.Publish(TopicTripCancelled, msg)
}
func SubscribeTripCancelled(bus Bus, group string, fn func(*eventspb.TripCancelled)) error {
	return bus.Subscribe(TopicTripCancelled, group, func(m proto.Message) {
		fn(m.(*eventspb.TripCancelled))
	})
}

func PublishDriverLocationUpdate(bus Bus, msg *eventspb.DriverLocationUpdate) error {
	return bus.Publish(TopicDriverLocationUpdate, msg)
}
func SubscribeDriverLocationUpdate(bus Bus, group string, fn func(*eventspb.DriverLocationUpdate)) error {
	return bus.Subscribe(TopicDriverLocationUpdate, group, func(m proto.Message) {
		fn(m.(*eventspb.DriverLocationUpdate))
	})
}
func SubscribeDriverLocationUpdateFromNow(bus Bus, fn func(*eventspb.DriverLocationUpdate)) error {
	return bus.SubscribeFromNow(TopicDriverLocationUpdate, func(m proto.Message) {
		fn(m.(*eventspb.DriverLocationUpdate))
	})
}

func PublishSurgeUpdated(bus Bus, msg *eventspb.SurgeUpdated) error {
	return bus.Publish(TopicSurgeUpdated, msg)
}
func SubscribeSurgeUpdated(bus Bus, group string, fn func(*eventspb.SurgeUpdated)) error {
	return bus.Subscribe(TopicSurgeUpdated, group, func(m proto.Message) {
		fn(m.(*eventspb.SurgeUpdated))
	})
}
