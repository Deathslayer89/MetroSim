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

// Counters holds totals from trip events. Pending and Active are derived from
// the rest and match the dispatcher once every topic is read to the same point.
type Counters struct {
	Requested int `json:"requested"`
	Matched   int `json:"matched"`
	PickedUp  int `json:"picked_up"`
	Completed int `json:"completed"`
	Abandoned int `json:"abandoned"`
	Cancelled int `json:"cancelled"`
	Pending   int `json:"pending"`
	Active    int `json:"active"`
}

func (c Counters) derived() Counters {
	c.Pending = c.Requested - c.Matched + c.Cancelled - c.Abandoned
	c.Active = c.Matched - c.Cancelled - c.Completed
	return c
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

// SubscribeToBus reads the trip and surge topics from the start, in no group,
// so counts survive a restart and every replica sees the whole fleet. Driver
// positions start at the newest record, since every tick resends them.
func (s *State) SubscribeToBus(bus events.Bus) error {
	const group = ""
	c := &s.counters
	inc := func(n *int) {
		s.mu.Lock()
		*n++
		s.mu.Unlock()
	}
	if err := events.SubscribeDriverLocationUpdateFromNow(bus, s.onDriverLocation); err != nil {
		return err
	}
	if err := events.SubscribeSurgeUpdated(bus, group, s.onSurge); err != nil {
		return err
	}
	if err := events.SubscribeTripRequested(bus, group, func(*eventspb.TripRequested) { inc(&c.Requested) }); err != nil {
		return err
	}
	if err := events.SubscribeTripMatched(bus, group, func(*eventspb.TripMatched) { inc(&c.Matched) }); err != nil {
		return err
	}
	if err := events.SubscribeTripPickedUp(bus, group, func(*eventspb.TripPickedUp) { inc(&c.PickedUp) }); err != nil {
		return err
	}
	if err := events.SubscribeTripCompleted(bus, group, func(*eventspb.TripCompleted) { inc(&c.Completed) }); err != nil {
		return err
	}
	if err := events.SubscribeTripAbandoned(bus, group, func(*eventspb.TripAbandoned) { inc(&c.Abandoned) }); err != nil {
		return err
	}
	return events.SubscribeTripCancelled(bus, group, func(*eventspb.TripCancelled) { inc(&c.Cancelled) })
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
		Counters: s.counters.derived(),
	}
	for _, v := range s.vehicles {
		out.Vehicles = append(out.Vehicles, v)
	}
	for _, c := range s.surge {
		out.Surge = append(out.Surge, c)
	}
	return out
}
