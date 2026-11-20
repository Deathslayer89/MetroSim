package liveview

import (
	"testing"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// Each trip event type has its own topic, so live-view can read a trip's completion before its match.
func TestCountersStayValidWhenCompletedArrivesBeforeMatched(t *testing.T) {
	bus := events.NewMemoryBus()
	s := NewState()
	if err := s.SubscribeToBus(bus); err != nil {
		t.Fatal(err)
	}
	_ = events.PublishTripRequested(bus, &eventspb.TripRequested{RequestId: 1})
	_ = events.PublishTripCompleted(bus, &eventspb.TripCompleted{RideId: 1})
	if c := s.Snapshot().Counters; c.Active < 0 || c.Pending < 0 {
		t.Errorf("completion read before the match: active %d, pending %d", c.Active, c.Pending)
	}
	_ = events.PublishTripPickedUp(bus, &eventspb.TripPickedUp{RideId: 1})
	_ = events.PublishTripMatched(bus, &eventspb.TripMatched{RideId: 1, RequestId: 1})
	c := s.Snapshot().Counters
	if c.Requested != 1 || c.Matched != 1 || c.PickedUp != 1 || c.Completed != 1 || c.Active != 0 || c.Pending != 0 {
		t.Errorf("after every event arrived: %+v", c)
	}
}
