package dispatcher

import (
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/uber/h3-go/v4"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/eta"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// tripKey keys a request's events by its ID. Each event type has its own topic,
// so this keeps one trip's events in order within a topic, not across topics:
// a consumer can see a trip's match before its request.
func tripKey(requestID int) string {
	return fmt.Sprintf("trip:%d", requestID)
}

type Request struct {
	ID              int
	PickupNode      int
	DestinationNode int
	RequestTime     time.Time
	AssignedDriver  int
	AssignmentTime  time.Time
	routeChecked    bool         // dropUnroutable found a route from pickup to dropoff
	declinedBy      map[int]bool // drivers who turned it down, so it isn't offered to them again
}

// decline records that a driver turned the request down.
func (r *Request) decline(driverID int) {
	if r.declinedBy == nil {
		r.declinedBy = make(map[int]bool)
	}
	r.declinedBy[driverID] = true
}

type Ride struct {
	ID           int
	Request      *Request
	Driver       *agent.Vehicle
	PickupTime   time.Time
	DropoffTime  time.Time
	State        RideState
	SurgeAtMatch float64
	PredictedETA float64 // trip-duration prediction at match time; 0 if no model
}

type RideState int

const (
	RideStateAssigned RideState = iota
	RideStatePickedUp
	RideStateCompleted
)

func (rs RideState) String() string {
	switch rs {
	case RideStateAssigned:
		return "assigned"
	case RideStatePickedUp:
		return "picked_up"
	case RideStateCompleted:
		return "completed"
	default:
		return "unknown"
	}
}

// Dispatcher matches requests to drivers through a Policy and publishes each
// trip's lifecycle on the event bus, without knowing who subscribes.
type Dispatcher struct {
	driverIndex  *H3DriverIndex
	pendingQueue []*Request
	activeRides  map[int]*Ride
	completed    int // rides completed; their details go out as TripCompleted
	nextRideID   int
	graph        *graph.Graph
	pathPlanner  *pathfinding.PathPlanner
	bus          events.Bus
	stamper      *events.Stamper
	policy       Policy
	surgeAt      func(lat, lon float64) float64
	etaEstimate  func(fromNode, toNode int) float64 // optional cheap matching-cost ETA; nil = route with A*
	etaModel     *eta.Model                         // nil disables predicted-ETA stamping

	driverBehavior DriverBehavior
	behaviorRNG    *rand.Rand
	declines       int
	cancellations  int
	lastTick       time.Time // when the previous Tick ran, for the time a cancel chance covers

	maxWait   time.Duration // a request unmatched this long is abandoned; 0 disables
	abandoned int

	reposition     Repositioning
	idleSince      map[int]time.Time
	recentPickups  []pickupSeen
	lastReposition time.Time
	repositioned   int

	mu sync.Mutex
}

// SetMaxWait drops requests left unmatched longer than wait, the way riders give
// up; 0 disables it. Without it an overloaded queue grows without bound.
func (d *Dispatcher) SetMaxWait(wait time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxWait = wait
}

// AbandonedCount returns how many requests left unserved: riders who gave up
// waiting, and requests with no route to their dropoff.
func (d *Dispatcher) AbandonedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.abandoned
}

// AssignedDriverIDs returns drivers on their way to a pickup.
func (d *Dispatcher) AssignedDriverIDs() map[int]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[int]bool, len(d.activeRides))
	for _, ride := range d.activeRides {
		if ride.State == RideStateAssigned && ride.Driver != nil {
			out[ride.Driver.ID] = true
		}
	}
	return out
}

// SetETAModel installs a trip-duration model. Its predictions go on each
// TripCompleted so traces can compare them with actual durations.
func (d *Dispatcher) SetETAModel(m *eta.Model) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.etaModel = m
}

// predictETA returns 0 without a model. It needs d.mu held.
func (d *Dispatcher) predictETA(pickupNode, dropoffNode int, surge float64, now time.Time) float64 {
	if d.etaModel == nil {
		return 0
	}
	p, err1 := d.graph.GetNode(pickupNode)
	q, err2 := d.graph.GetNode(dropoffNode)
	if err1 != nil || err2 != nil {
		return 0
	}
	dist := graph.EuclideanDistance(p, q)
	return d.etaModel.Predict(eta.Features(dist, surge, float64(now.UTC().Hour())))
}

// NewDispatcher returns a dispatcher that publishes events to bus. Stamper
// provides Meta for every outbound event; pass events.NewStamper(RunInfo{})
// in tests where labels don't matter.
func NewDispatcher(g *graph.Graph, planner *pathfinding.PathPlanner, bus events.Bus, stamper *events.Stamper) *Dispatcher {
	return &Dispatcher{
		driverIndex:  NewH3DriverIndex(),
		pendingQueue: make([]*Request, 0),
		activeRides:  make(map[int]*Ride),
		nextRideID:   1,
		graph:        g,
		pathPlanner:  planner,
		bus:          bus,
		stamper:      stamper,
		policy:       GreedyPolicy{},
	}
}

// SetPolicy swaps the matching policy from the next Tick on.
func (d *Dispatcher) SetPolicy(p Policy) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policy = p
}

// SetSurgeAt installs a surge lookup that policies can consult. nil disables.
func (d *Dispatcher) SetSurgeAt(f func(lat, lon float64) float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.surgeAt = f
}

// SetETAEstimate swaps per-candidate A* in the matching cost for a cheap
// estimate; the chosen driver is still routed with A*. nil routes every
// candidate.
func (d *Dispatcher) SetETAEstimate(f func(fromNode, toNode int) float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.etaEstimate = f
}

// DriverCells counts idle drivers per H3 cell.
func (d *Dispatcher) DriverCells() map[h3.Cell]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.driverIndex.CellsByDriverCount()
}

// OpenDemandCells counts open trips per H3 cell: pending requests plus rides
// whose driver hasn't reached the pickup yet.
func (d *Dispatcher) OpenDemandCells(res int) map[h3.Cell]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[h3.Cell]int)
	bucket := func(nodeID int) {
		n, err := d.graph.GetNode(nodeID)
		if err != nil {
			return
		}
		cell, err := h3.LatLngToCell(h3.LatLng{Lat: n.Lat, Lng: n.Lon}, res)
		if err != nil {
			return
		}
		out[cell]++
	}
	for _, req := range d.pendingQueue {
		bucket(req.PickupNode)
	}
	for _, ride := range d.activeRides {
		if ride.State == RideStateAssigned {
			bucket(ride.Request.PickupNode)
		}
	}
	return out
}

func (d *Dispatcher) SubmitRequest(req *Request) {
	d.mu.Lock()
	d.pendingQueue = append(d.pendingQueue, req)
	d.notePickup(req)
	d.mu.Unlock()
	meta := d.stamper.MetaFor(req.RequestTime)
	meta.PartitionKey = tripKey(req.ID)
	_ = events.PublishTripRequested(d.bus, &eventspb.TripRequested{
		Meta:            meta,
		RequestId:       int64(req.ID),
		PickupNode:      int64(req.PickupNode),
		DestinationNode: int64(req.DestinationNode),
	})
}

func (d *Dispatcher) Tick(vehicles map[int]*agent.Vehicle, currentTime time.Time, edgeWeights map[int]float64) {
	d.mu.Lock()
	// Publish after unlocking: a MemoryBus subscriber runs on this goroutine
	// and would deadlock if it called back into the dispatcher.
	abandonedEvents := append(d.expireStale(currentTime), d.dropUnroutable(currentTime)...)
	var sinceLast time.Duration
	if !d.lastTick.IsZero() {
		sinceLast = currentTime.Sub(d.lastTick)
	}
	d.lastTick = currentTime
	cancelledEvents, pickupEvents, completedEvents := d.updateActiveRides(vehicles, currentTime, sinceLast, edgeWeights)
	matchedEvents := d.runMatching(vehicles, currentTime, edgeWeights)
	d.repositionIdle(vehicles, currentTime, edgeWeights)
	d.mu.Unlock()

	for _, e := range abandonedEvents {
		_ = events.PublishTripAbandoned(d.bus, e)
	}
	// A cancelled request can be rematched this tick, so this goes out first.
	for _, e := range cancelledEvents {
		_ = events.PublishTripCancelled(d.bus, e)
	}
	for _, e := range pickupEvents {
		_ = events.PublishTripPickedUp(d.bus, e)
	}
	for _, e := range completedEvents {
		_ = events.PublishTripCompleted(d.bus, e)
	}
	for _, e := range matchedEvents {
		_ = events.PublishTripMatched(d.bus, e)
	}
}

// expireStale drops requests older than maxWait, keeping the queue's order,
// and returns an event for each. It needs d.mu held.
func (d *Dispatcher) expireStale(now time.Time) []*eventspb.TripAbandoned {
	if d.maxWait <= 0 || len(d.pendingQueue) == 0 {
		return nil
	}
	var out []*eventspb.TripAbandoned
	kept := d.pendingQueue[:0]
	for _, req := range d.pendingQueue {
		waited := now.Sub(req.RequestTime)
		if waited <= d.maxWait {
			kept = append(kept, req)
			continue
		}
		out = append(out, d.abandon(req, now))
	}
	d.pendingQueue = kept
	return out
}

// dropUnroutable abandons requests with no route from pickup to dropoff,
// checking each once. It needs d.mu held.
func (d *Dispatcher) dropUnroutable(now time.Time) []*eventspb.TripAbandoned {
	var out []*eventspb.TripAbandoned
	kept := d.pendingQueue[:0]
	for _, req := range d.pendingQueue {
		if !req.routeChecked {
			req.routeChecked = true
			if req.PickupNode != req.DestinationNode {
				if _, err := d.pathPlanner.FindPath(req.PickupNode, req.DestinationNode); err != nil {
					out = append(out, d.abandon(req, now))
					continue
				}
			}
		}
		kept = append(kept, req)
	}
	d.pendingQueue = kept
	return out
}

// abandon counts req as having left unserved and returns the event that says
// so. It needs d.mu held.
func (d *Dispatcher) abandon(req *Request, now time.Time) *eventspb.TripAbandoned {
	d.abandoned++
	meta := d.stamper.MetaFor(now)
	meta.PartitionKey = tripKey(req.ID)
	return &eventspb.TripAbandoned{
		Meta:       meta,
		RequestId:  int64(req.ID),
		PickupNode: int64(req.PickupNode),
		WaitedS:    now.Sub(req.RequestTime).Seconds(),
	}
}

// busyDrivers returns the drivers holding an active ride. A car already at its
// target stays Idle, so its state alone can't say whether it's free. It needs
// d.mu held.
func (d *Dispatcher) busyDrivers() map[int]bool {
	busy := make(map[int]bool, len(d.activeRides))
	for _, ride := range d.activeRides {
		busy[ride.Driver.ID] = true
	}
	return busy
}

// runMatching returns the TripMatched payloads to publish after d.mu is released.
func (d *Dispatcher) runMatching(vehicles map[int]*agent.Vehicle, currentTime time.Time, edgeWeights map[int]float64) []*eventspb.TripMatched {
	busy := d.busyDrivers()
	d.driverIndex.Clear()
	for _, vehicle := range vehicles {
		if vehicle.IsAvailable() && !busy[vehicle.ID] {
			lat, lon, err := vehicle.GetPosition()
			if err == nil {
				d.driverIndex.Insert(vehicle.ID, lat, lon)
			}
		}
	}

	assignments := d.policy.Match(MatchCtx{
		Pending:     d.pendingQueue,
		DriverIndex: d.driverIndex,
		Vehicles:    vehicles,
		EdgeWeights: edgeWeights,
		Graph:       d.graph,
		PathPlanner: d.pathPlanner,
		Now:         currentTime,
		SurgeAt:     d.surgeAt,
		ETAEstimate: d.etaEstimate,
		TripETA: func(pickup, dropoff int) float64 {
			return d.predictETA(pickup, dropoff, 1.0, currentTime)
		},
	})

	matched := make(map[int]struct{}, len(assignments))
	out := make([]*eventspb.TripMatched, 0, len(assignments))
	for _, a := range assignments {
		driver, ok := vehicles[a.DriverID]
		if !ok || !driver.IsAvailable() || busy[a.DriverID] {
			continue
		}
		// A declined request stays queued for other drivers, and the driver
		// stays available.
		if !d.accepts() {
			a.Request.decline(a.DriverID)
			continue
		}
		driver.SetDestination(a.Request.PickupNode)
		if err := driver.PlanRouteWithWeights(edgeWeights); err != nil {
			continue
		}
		a.Request.AssignedDriver = a.DriverID
		a.Request.AssignmentTime = currentTime

		surgeAtMatch := 1.0
		if d.surgeAt != nil {
			if pickup, err := d.graph.GetNode(a.Request.PickupNode); err == nil {
				surgeAtMatch = d.surgeAt(pickup.Lat, pickup.Lon)
			}
		}

		ride := &Ride{
			ID:           d.nextRideID,
			Request:      a.Request,
			Driver:       driver,
			State:        RideStateAssigned,
			SurgeAtMatch: surgeAtMatch,
			PredictedETA: d.predictETA(a.Request.PickupNode, a.Request.DestinationNode, surgeAtMatch, currentTime),
		}
		d.nextRideID++
		d.activeRides[ride.ID] = ride
		busy[a.DriverID] = true
		d.driverIndex.Remove(a.DriverID)
		matched[a.Request.ID] = struct{}{}
		meta := d.stamper.MetaFor(currentTime)
		meta.PartitionKey = tripKey(a.Request.ID)
		out = append(out, &eventspb.TripMatched{
			Meta:           meta,
			RideId:         int64(ride.ID),
			RequestId:      int64(a.Request.ID),
			DriverId:       int64(a.DriverID),
			PickupNode:     int64(a.Request.PickupNode),
			SurgeAtMatch:   surgeAtMatch,
			EtaCostSeconds: a.Cost,
		})
	}

	remaining := make([]*Request, 0, len(d.pendingQueue)-len(matched))
	for _, req := range d.pendingQueue {
		if _, m := matched[req.ID]; !m {
			remaining = append(remaining, req)
		}
	}
	d.pendingQueue = remaining
	return out
}

// updateActiveRides advances each ride and returns the events to publish once
// d.mu is released. sinceLast is the simulated time since the previous tick.
func (d *Dispatcher) updateActiveRides(vehicles map[int]*agent.Vehicle, currentTime time.Time, sinceLast time.Duration, edgeWeights map[int]float64) ([]*eventspb.TripCancelled, []*eventspb.TripPickedUp, []*eventspb.TripCompleted) {
	var cancels []*eventspb.TripCancelled
	var pickups []*eventspb.TripPickedUp
	var completes []*eventspb.TripCompleted
	// Ride-ID order keeps RNG draws and completion order the same every run.
	rideIDs := make([]int, 0, len(d.activeRides))
	for id := range d.activeRides {
		rideIDs = append(rideIDs, id)
	}
	sort.Ints(rideIDs)
	for _, rideID := range rideIDs {
		ride := d.activeRides[rideID]
		driver := ride.Driver

		switch ride.State {
		case RideStateAssigned:
			// A cancelling driver is freed where it is. The request is requeued,
			// and not offered to that driver again.
			if d.cancels(sinceLast) {
				driver.Abort()
				ride.Request.AssignedDriver = 0
				ride.Request.decline(driver.ID)
				d.pendingQueue = append(d.pendingQueue, ride.Request)
				delete(d.activeRides, rideID)
				meta := d.stamper.MetaFor(currentTime)
				meta.PartitionKey = tripKey(ride.Request.ID)
				cancels = append(cancels, &eventspb.TripCancelled{
					Meta:      meta,
					RideId:    int64(ride.ID),
					RequestId: int64(ride.Request.ID),
					DriverId:  int64(driver.ID),
				})
				continue
			}
			if driver.IsIdle() && driver.CurrentNode == ride.Request.PickupNode {
				// Plan the dropoff before marking PickedUp, so a failure can't
				// leave a ride that never completes.
				driver.SetDestination(ride.Request.DestinationNode)
				if err := driver.PlanRouteWithWeights(edgeWeights); err != nil {
					log.Printf("dispatcher: ride %d dropoff unreachable, dropping: %v", ride.ID, err)
					delete(d.activeRides, rideID)
					continue
				}
				ride.PickupTime = currentTime
				ride.State = RideStatePickedUp

				puMeta := d.stamper.MetaFor(currentTime)
				puMeta.PartitionKey = tripKey(ride.Request.ID)
				pickups = append(pickups, &eventspb.TripPickedUp{
					Meta:     puMeta,
					RideId:   int64(ride.ID),
					DriverId: int64(driver.ID),
				})
			}

		case RideStatePickedUp:
			if driver.IsIdle() && driver.CurrentNode == ride.Request.DestinationNode {
				ride.DropoffTime = currentTime
				ride.State = RideStateCompleted
				d.completed++
				delete(d.activeRides, rideID)

				pickup, perr := d.graph.GetNode(ride.Request.PickupNode)
				dropoff, derr := d.graph.GetNode(ride.Request.DestinationNode)
				if perr != nil || derr != nil {
					continue
				}
				cMeta := d.stamper.MetaFor(currentTime)
				cMeta.PartitionKey = tripKey(ride.Request.ID)
				completes = append(completes, &eventspb.TripCompleted{
					Meta:          cMeta,
					RideId:        int64(ride.ID),
					RequestId:     int64(ride.Request.ID),
					DriverId:      int64(driver.ID),
					RequestTime:   timestamppb.New(ride.Request.RequestTime),
					MatchTime:     timestamppb.New(ride.Request.AssignmentTime),
					PickupTime:    timestamppb.New(ride.PickupTime),
					DropoffTime:   timestamppb.New(ride.DropoffTime),
					PickupNode:    int64(ride.Request.PickupNode),
					DropoffNode:   int64(ride.Request.DestinationNode),
					PickupLat:     pickup.Lat,
					PickupLon:     pickup.Lon,
					DropoffLat:    dropoff.Lat,
					DropoffLon:    dropoff.Lon,
					SurgeAtMatch:  ride.SurgeAtMatch,
					EtaPredictedS: ride.PredictedETA,
				})
			}
		}
	}
	return cancels, pickups, completes
}

func (d *Dispatcher) GetPendingCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pendingQueue)
}

func (d *Dispatcher) GetActiveRideCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.activeRides)
}

// CompletedCount returns how many rides have been completed. The rides aren't
// kept; TripCompleted carries their details.
func (d *Dispatcher) CompletedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.completed
}

// RidesInProgress returns active rides whose rider is aboard, in ride-ID order.
func (d *Dispatcher) RidesInProgress() []*Ride {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*Ride
	for _, r := range d.activeRides {
		if r.State == RideStatePickedUp {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Waiting returns the requests whose rider hasn't been picked up, queued or
// with a driver on the way, in request-ID order.
func (d *Dispatcher) Waiting() []*Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]*Request(nil), d.pendingQueue...)
	for _, r := range d.activeRides {
		if r.State == RideStateAssigned {
			out = append(out, r.Request)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
