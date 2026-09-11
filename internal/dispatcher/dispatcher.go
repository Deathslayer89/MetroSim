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

// pickupKey keys a request's events by the H3 r6 cell of its pickup, so one
// trip's events stay in order on one partition and nearby trips share it. It
// falls back to the request ID when the pickup node is unknown.
func (d *Dispatcher) pickupKey(requestID, pickupNode int) string {
	if n, err := d.graph.GetNode(pickupNode); err == nil {
		if cell, err := h3.LatLngToCell(h3.LatLng{Lat: n.Lat, Lng: n.Lon}, 6); err == nil {
			return cell.String()
		}
	}
	return fmt.Sprintf("trip:%d", requestID)
}

type Request struct {
	ID              int
	PickupNode      int
	DestinationNode int
	RequestTime     time.Time
	AssignedDriver  int
	AssignmentTime  time.Time
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
	driverIndex    *H3DriverIndex
	pendingQueue   []*Request
	activeRides    map[int]*Ride
	completedRides []*Ride
	nextRideID     int
	graph          *graph.Graph
	pathPlanner    *pathfinding.PathPlanner
	bus            events.Bus
	stamper        *events.Stamper
	policy         Policy
	surgeAt        func(lat, lon float64) float64
	etaEstimate    func(fromNode, toNode int) float64 // optional cheap matching-cost ETA; nil = route with A*
	etaModel       *eta.Model                         // nil disables predicted-ETA stamping

	driverBehavior DriverBehavior
	behaviorRNG    *rand.Rand
	declines       int
	cancellations  int

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

// AbandonedCount returns how many requests have abandoned the queue unserved.
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
		driverIndex:    NewH3DriverIndex(),
		pendingQueue:   make([]*Request, 0),
		activeRides:    make(map[int]*Ride),
		completedRides: make([]*Ride, 0),
		nextRideID:     1,
		graph:          g,
		pathPlanner:    planner,
		bus:            bus,
		stamper:        stamper,
		policy:         GreedyPolicy{},
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
	meta.PartitionKey = d.pickupKey(req.ID, req.PickupNode)
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
	abandonedEvents := d.expireStale(currentTime)
	cancelledEvents, pickupEvents, completedEvents := d.updateActiveRides(vehicles, currentTime, edgeWeights)
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
		d.abandoned++
		meta := d.stamper.MetaFor(now)
		meta.PartitionKey = d.pickupKey(req.ID, req.PickupNode)
		out = append(out, &eventspb.TripAbandoned{
			Meta:       meta,
			RequestId:  int64(req.ID),
			PickupNode: int64(req.PickupNode),
			WaitedS:    waited.Seconds(),
		})
	}
	d.pendingQueue = kept
	return out
}

// runMatching returns the TripMatched payloads to publish after d.mu is released.
func (d *Dispatcher) runMatching(vehicles map[int]*agent.Vehicle, currentTime time.Time, edgeWeights map[int]float64) []*eventspb.TripMatched {
	d.driverIndex.Clear()
	for _, vehicle := range vehicles {
		if vehicle.IsAvailable() {
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
		if !ok || !driver.IsAvailable() {
			continue
		}
		// A declined request stays queued and the driver stays available.
		if !d.accepts() {
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
		d.driverIndex.Remove(a.DriverID)
		matched[a.Request.ID] = struct{}{}
		meta := d.stamper.MetaFor(currentTime)
		meta.PartitionKey = d.pickupKey(a.Request.ID, a.Request.PickupNode)
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
// d.mu is released.
func (d *Dispatcher) updateActiveRides(vehicles map[int]*agent.Vehicle, currentTime time.Time, edgeWeights map[int]float64) ([]*eventspb.TripCancelled, []*eventspb.TripPickedUp, []*eventspb.TripCompleted) {
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
			// A cancelling driver is freed where it is; the request is requeued.
			if d.cancels() {
				driver.Abort()
				ride.Request.AssignedDriver = 0
				d.pendingQueue = append(d.pendingQueue, ride.Request)
				delete(d.activeRides, rideID)
				meta := d.stamper.MetaFor(currentTime)
				meta.PartitionKey = d.pickupKey(ride.Request.ID, ride.Request.PickupNode)
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
				puMeta.PartitionKey = d.pickupKey(ride.Request.ID, ride.Request.PickupNode)
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
				d.completedRides = append(d.completedRides, ride)
				delete(d.activeRides, rideID)

				pickup, perr := d.graph.GetNode(ride.Request.PickupNode)
				dropoff, derr := d.graph.GetNode(ride.Request.DestinationNode)
				if perr != nil || derr != nil {
					continue
				}
				cMeta := d.stamper.MetaFor(currentTime)
				cMeta.PartitionKey = d.pickupKey(ride.Request.ID, ride.Request.PickupNode)
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

func (d *Dispatcher) GetCompletedRides() []*Ride {
	d.mu.Lock()
	defer d.mu.Unlock()
	rides := make([]*Ride, len(d.completedRides))
	copy(rides, d.completedRides)
	return rides
}
