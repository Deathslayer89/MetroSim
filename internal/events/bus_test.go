package events

import (
	"testing"

	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

func TestMemoryBusDelivers(t *testing.T) {
	bus := NewMemoryBus()
	var got []int64
	if err := SubscribeTripMatched(bus, "g1", func(m *eventspb.TripMatched) {
		got = append(got, m.RideId)
	}); err != nil {
		t.Fatal(err)
	}
	_ = PublishTripMatched(bus, &eventspb.TripMatched{RideId: 1})
	_ = PublishTripMatched(bus, &eventspb.TripMatched{RideId: 2})
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("want [1 2], got %v", got)
	}
}

func TestMemoryBusTopicIsolation(t *testing.T) {
	bus := NewMemoryBus()
	var matched, completed int
	_ = SubscribeTripMatched(bus, "g", func(*eventspb.TripMatched) { matched++ })
	_ = SubscribeTripCompleted(bus, "g", func(*eventspb.TripCompleted) { completed++ })
	_ = PublishTripMatched(bus, &eventspb.TripMatched{RideId: 1})
	_ = PublishTripCompleted(bus, &eventspb.TripCompleted{RideId: 1})
	_ = PublishTripCompleted(bus, &eventspb.TripCompleted{RideId: 2})
	if matched != 1 || completed != 2 {
		t.Errorf("matched=%d completed=%d (want 1, 2)", matched, completed)
	}
}

func TestMemoryBusMultipleSubscribersSameTopic(t *testing.T) {
	bus := NewMemoryBus()
	var a, b int
	_ = SubscribeTripCompleted(bus, "ga", func(*eventspb.TripCompleted) { a++ })
	_ = SubscribeTripCompleted(bus, "gb", func(*eventspb.TripCompleted) { b++ })
	_ = PublishTripCompleted(bus, &eventspb.TripCompleted{})
	if a != 1 || b != 1 {
		t.Errorf("both subscribers should fire: a=%d b=%d", a, b)
	}
}

func TestMemoryBusNoSubscribersIsNoOp(t *testing.T) {
	bus := NewMemoryBus()
	if err := PublishTripMatched(bus, &eventspb.TripMatched{RideId: 99}); err != nil {
		t.Errorf("publish with no subscribers: %v", err)
	}
}

// Stamper produces monotonic event_ids and copies RunInfo into Meta.
func TestStamperStampsRunInfoAndMonotonicIDs(t *testing.T) {
	s := NewStamper(RunInfo{Scenario: "smoke", Policy: "batch", Seed: 7})
	m1 := s.MetaFor(zeroTime())
	m2 := s.MetaFor(zeroTime())
	if m1.EventId != 1 || m2.EventId != 2 {
		t.Errorf("ids should be monotonic from 1: got %d, %d", m1.EventId, m2.EventId)
	}
	if m1.Scenario != "smoke" || m1.Policy != "batch" || m1.Seed != 7 {
		t.Errorf("RunInfo not stamped: %+v", m1)
	}
}

func TestStamperSetRunInfoTakesEffect(t *testing.T) {
	s := NewStamper(RunInfo{Scenario: "a"})
	s.SetRunInfo(RunInfo{Scenario: "b"})
	if s.MetaFor(zeroTime()).Scenario != "b" {
		t.Error("SetRunInfo did not take effect")
	}
}
