// Package liveview rebuilds marketplace state from the event stream alone and
// pushes it to websocket clients, so it runs on either bus.
package liveview

import (
	"sync"
	"time"

	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// Vehicle is a snapshot of one driver's last-known position/state.
type Vehicle struct {
	ID    int     `json:"id"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	State string  `json:"state"`
}

// SurgeCell is one surge-active H3 cell with its current multiplier.
type SurgeCell struct {
	Cell       string      `json:"cell"`
	Boundary   [][]float64 `json:"boundary"`
	Multiplier float64     `json:"multiplier"`
}

// Counters tracks rolling totals derived from trip lifecycle events.
type Counters struct {
	Requested int `json:"requested"`
	Matched   int `json:"matched"`
	PickedUp  int `json:"picked_up"`
	Completed int `json:"completed"`
}

// State is the marketplace's in-memory projection from the event stream.
type State struct {
	mu       sync.RWMutex
	vehicles map[int]Vehicle
	surge    map[string]SurgeCell
	counters Counters
	lastSeen time.Time
}

func NewState() *State {
	return &State{
		vehicles: make(map[int]Vehicle),
		surge:    make(map[string]SurgeCell),
	}
}

// SubscribeToBus wires this State to the bus under the "live-view" consumer
// group. Subscribes to driver locations, surge updates, and the four trip
// lifecycle topics.
func (s *State) SubscribeToBus(bus events.Bus) error {
	if err := events.SubscribeDriverLocationUpdate(bus, "live-view", s.onDriverLocation); err != nil {
		return err
	}
	if err := events.SubscribeSurgeUpdated(bus, "live-view", s.onSurge); err != nil {
		return err
	}
	if err := events.SubscribeTripRequested(bus, "live-view", func(*eventspb.TripRequested) {
		s.mu.Lock()
		s.counters.Requested++
		s.mu.Unlock()
	}); err != nil {
		return err
	}
	if err := events.SubscribeTripMatched(bus, "live-view", func(*eventspb.TripMatched) {
		s.mu.Lock()
		s.counters.Matched++
		s.mu.Unlock()
	}); err != nil {
		return err
	}
	if err := events.SubscribeTripPickedUp(bus, "live-view", func(*eventspb.TripPickedUp) {
		s.mu.Lock()
		s.counters.PickedUp++
		s.mu.Unlock()
	}); err != nil {
		return err
	}
	return events.SubscribeTripCompleted(bus, "live-view", func(*eventspb.TripCompleted) {
		s.mu.Lock()
		s.counters.Completed++
		s.mu.Unlock()
	})
}

func (s *State) onDriverLocation(e *eventspb.DriverLocationUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vehicles[int(e.DriverId)] = Vehicle{
		ID:    int(e.DriverId),
		Lat:   e.Lat,
		Lon:   e.Lon,
		State: e.State,
	}
	if e.Meta != nil && e.Meta.Time != nil {
		s.lastSeen = e.Meta.Time.AsTime()
	}
}

func (s *State) onSurge(e *eventspb.SurgeUpdated) {
	cell := h3.Cell(h3.IndexFromString(e.H3Cell))
	boundary, err := cell.Boundary()
	if err != nil {
		return
	}
	pts := make([][]float64, 0, len(boundary))
	for _, ll := range boundary {
		pts = append(pts, []float64{ll.Lat, ll.Lng})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Multiplier <= 1.0001 {
		delete(s.surge, e.H3Cell)
		return
	}
	s.surge[e.H3Cell] = SurgeCell{Cell: e.H3Cell, Boundary: pts, Multiplier: e.Multiplier}
}

// Snapshot is one websocket frame.
type Snapshot struct {
	Time     string      `json:"time"`
	Vehicles []Vehicle   `json:"vehicles"`
	Surge    []SurgeCell `json:"surge,omitempty"`
	Counters Counters    `json:"counters"`
}

// Snapshot copies the current state.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{
		Time:     s.lastSeen.UTC().Format(time.RFC3339Nano),
		Vehicles: make([]Vehicle, 0, len(s.vehicles)),
		Surge:    make([]SurgeCell, 0, len(s.surge)),
		Counters: s.counters,
	}
	for _, v := range s.vehicles {
		out.Vehicles = append(out.Vehicles, v)
	}
	for _, c := range s.surge {
		out.Surge = append(out.Surge, c)
	}
	return out
}
