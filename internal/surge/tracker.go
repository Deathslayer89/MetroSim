// Package surge maintains per-H3-cell surge multipliers from live supply/demand
// imbalance, smoothed with an EMA so the readout doesn't strobe.
package surge

import (
	"math"
	"sync"
	"time"

	"github.com/uber/h3-go/v4"
)

// Resolution is H3 r8, cells of about 0.74 square kilometers, a neighborhood.
const Resolution = 8

const (
	MinMultiplier = 1.0
	MaxMultiplier = 3.0

	// EMATau is the smoothing time constant: a step in raw surge is 63%
	// absorbed after one tau and 95% after three.
	EMATau = 60 * time.Second
)

// Tracker is safe for concurrent Snapshot/Multiplier reads with one Update writer.
type Tracker struct {
	mu       sync.RWMutex
	cells    map[h3.Cell]float64
	lastTick time.Time
}

func NewTracker() *Tracker {
	return &Tracker{cells: make(map[h3.Cell]float64)}
}

// Update recomputes raw surge per cell from open requests / max(idle drivers, 1),
// clamps to [Min,Max], then applies EMA smoothing with dt since the previous
// Update. Cells that decay back to baseline are dropped from the map.
func (t *Tracker) Update(now time.Time, openRequests, idleDrivers map[h3.Cell]int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var alpha float64
	if !t.lastTick.IsZero() {
		dt := now.Sub(t.lastTick).Seconds()
		if dt > 0 {
			alpha = 1 - math.Exp(-dt/EMATau.Seconds())
		}
	} else {
		alpha = 1 // first Update: nothing to blend with
	}
	t.lastTick = now

	cells := make(map[h3.Cell]struct{}, len(openRequests)+len(idleDrivers)+len(t.cells))
	for c := range openRequests {
		cells[c] = struct{}{}
	}
	for c := range idleDrivers {
		cells[c] = struct{}{}
	}
	for c := range t.cells {
		cells[c] = struct{}{}
	}

	for c := range cells {
		req := openRequests[c]
		drv := idleDrivers[c]
		if drv < 1 {
			drv = 1
		}
		raw := float64(req) / float64(drv)
		if raw < MinMultiplier {
			raw = MinMultiplier
		} else if raw > MaxMultiplier {
			raw = MaxMultiplier
		}
		prev, seen := t.cells[c]
		if !seen {
			prev = MinMultiplier
		}
		smoothed := prev + alpha*(raw-prev)
		if smoothed <= MinMultiplier+1e-3 {
			delete(t.cells, c)
		} else {
			t.cells[c] = smoothed
		}
	}
}

func (t *Tracker) Multiplier(c h3.Cell) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if v, ok := t.cells[c]; ok {
		return v
	}
	return MinMultiplier
}

// MultiplierAt returns the surge at a lat/lon by bucketing into the tracker's resolution.
func (t *Tracker) MultiplierAt(lat, lon float64) float64 {
	cell, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lon}, Resolution)
	if err != nil {
		return MinMultiplier
	}
	return t.Multiplier(cell)
}

// Snapshot returns a value copy of all active surge cells (multiplier > 1).
func (t *Tracker) Snapshot() map[h3.Cell]float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[h3.Cell]float64, len(t.cells))
	for c, v := range t.cells {
		out[c] = v
	}
	return out
}

// BucketLatLons groups points into per-cell counts at the given H3 resolution.
func BucketLatLons(points []LatLon, res int) map[h3.Cell]int {
	out := make(map[h3.Cell]int)
	for _, p := range points {
		cell, err := h3.LatLngToCell(h3.LatLng{Lat: p.Lat, Lng: p.Lon}, res)
		if err != nil {
			continue
		}
		out[cell]++
	}
	return out
}

type LatLon struct {
	Lat float64
	Lon float64
}
