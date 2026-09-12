package dispatcher

import (
	"sort"
	"time"

	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/agent"
)

// repositionResolution is H3 r8, the cells surge uses.
const repositionResolution = 8

// Repositioning sends cars that have sat idle for IdleAfter toward the cell,
// within Rings rings of their own, where recent pickups most outnumber the
// available cars. Recent means requested within Window; it runs every Every.
type Repositioning struct {
	IdleAfter time.Duration
	Every     time.Duration
	Window    time.Duration
	Rings     int
}

// DefaultRepositioning looks every 30 s at the last 15 minutes of pickups, up
// to 3 rings (about 2.5 km) away.
func DefaultRepositioning(idleAfter time.Duration) Repositioning {
	return Repositioning{IdleAfter: idleAfter, Every: 30 * time.Second, Window: 15 * time.Minute, Rings: 3}
}

type pickupSeen struct {
	at   time.Time
	cell h3.Cell
	node int
}

// SetRepositioning turns repositioning on; a zero IdleAfter turns it off.
func (d *Dispatcher) SetRepositioning(r Repositioning) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reposition = r
	d.idleSince = make(map[int]time.Time)
}

// RepositionCount returns how many times a car has been sent somewhere empty.
func (d *Dispatcher) RepositionCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.repositioned
}

func (d *Dispatcher) cellOf(node int) (h3.Cell, bool) {
	n, err := d.graph.GetNode(node)
	if err != nil {
		return 0, false
	}
	c, err := h3.LatLngToCell(h3.LatLng{Lat: n.Lat, Lng: n.Lon}, repositionResolution)
	return c, err == nil
}

// notePickup needs d.mu held.
func (d *Dispatcher) notePickup(req *Request) {
	if d.reposition.IdleAfter <= 0 {
		return
	}
	if c, ok := d.cellOf(req.PickupNode); ok {
		d.recentPickups = append(d.recentPickups, pickupSeen{req.RequestTime, c, req.PickupNode})
	}
}

// repositionIdle needs d.mu held.
func (d *Dispatcher) repositionIdle(vehicles map[int]*agent.Vehicle, now time.Time, weights map[int]float64) {
	r := d.reposition
	if r.IdleAfter <= 0 {
		return
	}
	busy := d.busyDrivers()
	for id, v := range vehicles {
		if !v.IsIdle() || busy[id] {
			delete(d.idleSince, id)
		} else if _, ok := d.idleSince[id]; !ok {
			d.idleSince[id] = now
		}
	}
	if !d.lastReposition.IsZero() && now.Sub(d.lastReposition) < r.Every {
		return
	}
	d.lastReposition = now

	drop := 0
	for drop < len(d.recentPickups) && now.Sub(d.recentPickups[drop].at) > r.Window {
		drop++
	}
	d.recentPickups = d.recentPickups[drop:]
	if len(d.recentPickups) == 0 {
		return
	}

	// gap is recent pickups minus the cars in, or already heading to, a cell.
	gap := make(map[h3.Cell]int)
	target := make(map[h3.Cell]int)
	for _, p := range d.recentPickups {
		gap[p.cell]++
		target[p.cell] = p.node
	}
	ids := make([]int, 0, len(vehicles))
	for id, v := range vehicles {
		if !v.IsAvailable() || busy[id] {
			continue
		}
		ids = append(ids, id)
		node := v.CurrentNode
		if v.State == agent.StateRepositioning {
			node = v.Destination
		}
		if c, ok := d.cellOf(node); ok {
			gap[c]--
		}
	}
	sort.Ints(ids)

	for _, id := range ids {
		since, ok := d.idleSince[id]
		if !ok || now.Sub(since) < r.IdleAfter {
			continue
		}
		v := vehicles[id]
		here, ok := d.cellOf(v.CurrentNode)
		if !ok {
			continue
		}
		disk, err := here.GridDisk(r.Rings)
		if err != nil {
			continue
		}
		best := here
		for _, c := range disk {
			if gap[c] > gap[best] {
				best = c
			}
		}
		if best == here || gap[best] <= 0 {
			continue
		}
		if err := v.Reposition(target[best], weights); err != nil {
			continue
		}
		gap[best]--
		gap[here]++
		delete(d.idleSince, id)
		d.repositioned++
	}
}
