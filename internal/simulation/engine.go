package simulation

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/metrics"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
	"github.com/Deathslayer89/MetroSim/internal/promexport"
	"github.com/Deathslayer89/MetroSim/internal/surge"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// surgePublishEpsilon is the smallest multiplier change worth republishing for
// a cell that has already been sent.
const surgePublishEpsilon = 0.05

// replanEvery spaces congestion replans a simulated second apart at any speed,
// which keeps A* off the per-tick path.
const replanEvery = time.Second

type SimulationState int32

const (
	StateStopped SimulationState = iota
	StateRunning
	StatePaused
)

type cmdKind uint8

const (
	cmdPause cmdKind = iota
	cmdResume
	cmdSetSpeed
)

type command struct {
	kind cmdKind
	arg  float64
}

// ArrivalSource feeds new ride requests into the engine. Called once per Tick
// with the elapsed sim time since Run started and the tick's dt.
type ArrivalSource interface {
	NextArrivals(elapsed, dt time.Duration) []*dispatcher.Request
}

type Engine struct {
	graph            *graph.Graph
	trafficModel     *traffic.TrafficModel
	pathPlanner      *pathfinding.PathPlanner
	dispatcher       *dispatcher.Dispatcher
	metricsCollector *metrics.MetricsCollector
	bus              events.Bus
	outbox           *outbox // the dispatcher's bus; flushed once e.mu is released
	stamper          *events.Stamper
	arrivals         ArrivalSource
	surge            *surge.Tracker

	vehicles      map[int]*agent.Vehicle
	nextVehicleID int

	startTime       time.Time
	currentTime     time.Time
	tickCount       int64
	tickRate        float64
	speedMultiplier float64

	// lastSurgePublished is the multiplier last sent per cell; a cell is resent
	// only after it moves by more than surgePublishEpsilon.
	lastSurgePublished map[h3.Cell]float64

	// pendingReplan collects edges changed since the last replan pass, so the
	// ticks between passes don't lose changes.
	pendingReplan map[int]float64
	lastReplan    time.Time

	state    atomic.Int32
	cmds     chan command
	stop     chan struct{}
	stopOnce sync.Once

	mu sync.RWMutex
}

type Config struct {
	Graph            *graph.Graph
	CongestionParams traffic.CongestionParams
	TickRate         float64
	SpeedMultiplier  float64
}

func NewEngine(cfg Config) *Engine {
	return NewEngineWithBus(cfg, events.NewMemoryBus())
}

// NewEngineWithBus is NewEngine on a caller-owned bus, typically Kafka.
func NewEngineWithBus(cfg Config, bus events.Bus) *Engine {
	if cfg.TickRate == 0 {
		cfg.TickRate = 10.0
	}
	if !validSpeed(cfg.SpeedMultiplier) {
		cfg.SpeedMultiplier = 1.0
	}

	trafficModel := traffic.NewTrafficModel(cfg.Graph, cfg.CongestionParams)
	pathPlanner := pathfinding.NewPathPlanner(cfg.Graph, nil)
	metricsCollector := metrics.NewMetricsCollector()
	stamper := events.NewStamper(events.RunInfo{})
	out := &outbox{Bus: bus}
	dispatcher := dispatcher.NewDispatcher(cfg.Graph, pathPlanner, out, stamper)

	e := &Engine{
		graph:            cfg.Graph,
		trafficModel:     trafficModel,
		pathPlanner:      pathPlanner,
		dispatcher:       dispatcher,
		metricsCollector: metricsCollector,
		bus:              bus,
		outbox:           out,
		stamper:          stamper,
		vehicles:         make(map[int]*agent.Vehicle),
		nextVehicleID:    1,
		startTime:        time.Now(),
		currentTime:      time.Now(),
		tickRate:         cfg.TickRate,
		speedMultiplier:  cfg.SpeedMultiplier,
		cmds:             make(chan command, 8),
		stop:             make(chan struct{}),
		pendingReplan:    make(map[int]float64),
	}
	e.state.Store(int32(StateStopped))
	return e
}

func (e *Engine) SpawnVehicle(nodeID int) *agent.Vehicle {
	e.mu.Lock()
	defer e.mu.Unlock()

	vehicle := agent.NewVehicle(e.nextVehicleID, nodeID, e.graph, e.pathPlanner)
	e.vehicles[vehicle.ID] = vehicle
	e.nextVehicleID++

	return vehicle
}

// SubmitRequest stamps req.RequestTime with sim time. Safe to call during Run.
func (e *Engine) SubmitRequest(req *dispatcher.Request) {
	e.mu.RLock()
	req.RequestTime = e.currentTime
	e.mu.RUnlock()
	e.dispatcher.SubmitRequest(req)
	e.outbox.flush()
}

// SetArrivalSource installs a generator that the Engine polls each Tick for new
// ride requests. Safe to call before Run; not safe to swap during Run.
func (e *Engine) SetArrivalSource(s ArrivalSource) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.arrivals = s
}

// EnableSurge attaches a surge tracker. Engine updates it once per Tick and
// makes the lookup available to the dispatcher's policy.
func (e *Engine) EnableSurge() *surge.Tracker {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.surge == nil {
		e.surge = surge.NewTracker()
		e.dispatcher.SetSurgeAt(e.surge.MultiplierAt)
	}
	return e.surge
}

func (e *Engine) GetSurge() *surge.Tracker { return e.surge }
func (e *Engine) Bus() events.Bus          { return e.bus }

// SetRunInfo stamps subsequent events with the given run labels.
func (e *Engine) SetRunInfo(info events.RunInfo) { e.stamper.SetRunInfo(info) }

// SetStartTime overrides the sim epoch (default time.Now()). Use a fixed value
// when running A/B comparisons that need byte-identical trip timestamps.
func (e *Engine) SetStartTime(t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.startTime = t
	e.currentTime = t
}

// driverPublish and surgePublish are copies taken under e.mu so publishing can
// happen after it's released; holding it through them starves readers.
type driverPublish struct {
	id    int
	lat   float64
	lon   float64
	state string
}

type surgePublish struct {
	cell       string
	multiplier float64
}

func (e *Engine) Tick() {
	e.mu.Lock()

	deltaTime := 1.0 / e.tickRate * e.speedMultiplier
	dt := time.Duration(deltaTime * float64(time.Second))

	if e.arrivals != nil {
		elapsed := e.currentTime.Sub(e.startTime)
		for _, req := range e.arrivals.NextArrivals(elapsed, dt) {
			req.RequestTime = e.currentTime
			e.dispatcher.SubmitRequest(req)
		}
	}

	vehicleList := make([]*agent.Vehicle, 0, len(e.vehicles))
	for _, v := range e.vehicles {
		vehicleList = append(vehicleList, v)
	}
	e.trafficModel.UpdateDensities(vehicleList)

	changedEdges := e.trafficModel.ComputeEdgeWeights()
	allWeights := e.trafficModel.GetEdgeWeights()
	for id, w := range changedEdges {
		e.pendingReplan[id] = w
	}
	// Replan at most once per vehicle, a simulated second apart.
	if len(e.pendingReplan) > 0 && (e.lastReplan.IsZero() || e.currentTime.Sub(e.lastReplan) >= replanEvery) {
		for _, vehicle := range e.vehicles {
			vehicle.MaybeReplan(e.pendingReplan, allWeights)
		}
		e.pendingReplan = make(map[int]float64)
		e.lastReplan = e.currentTime
	}

	for _, vehicle := range e.vehicles {
		vehicle.MoveWithWeights(deltaTime, allWeights)
	}

	e.dispatcher.Tick(e.vehicles, e.currentTime, allWeights)

	var available int
	driverPoints := make([]surge.LatLon, 0, len(e.vehicles))
	for _, v := range e.vehicles {
		if v.IsAvailable() {
			available++
			lat, lon, err := v.GetPosition()
			if err == nil {
				driverPoints = append(driverPoints, surge.LatLon{Lat: lat, Lon: lon})
			}
		}
	}

	if e.surge != nil {
		e.surge.Update(
			e.currentTime,
			e.dispatcher.OpenDemandCells(surge.Resolution),
			surge.BucketLatLons(driverPoints, surge.Resolution),
		)
	}

	promexport.SetIdleDrivers(available)
	promexport.SetActiveTrips(e.dispatcher.GetActiveRideCount())
	promexport.SetPendingRequests(e.dispatcher.GetPendingCount())
	promexport.SetAbandonedRequests(e.dispatcher.AbandonedCount())

	assigned := e.dispatcher.AssignedDriverIDs()
	driverEvents := make([]driverPublish, 0, len(e.vehicles))
	for _, v := range e.vehicles {
		lat, lon, err := v.GetPosition()
		if err != nil {
			continue
		}
		state := v.State.String()
		if assigned[v.ID] {
			state = "assigned"
		}
		driverEvents = append(driverEvents, driverPublish{id: v.ID, lat: lat, lon: lon, state: state})
	}

	var surgeEvents []surgePublish
	if e.surge != nil {
		snap := e.surge.Snapshot()
		if e.lastSurgePublished == nil {
			e.lastSurgePublished = make(map[h3.Cell]float64)
		}
		surgeEvents = make([]surgePublish, 0)
		var hot int
		for cell, mult := range snap {
			if mult > 1.05 {
				hot++
			}
			prev, seen := e.lastSurgePublished[cell]
			if !seen || math.Abs(mult-prev) > surgePublishEpsilon {
				surgeEvents = append(surgeEvents, surgePublish{cell: cell.String(), multiplier: mult})
				e.lastSurgePublished[cell] = mult
			}
		}
		// A cell the tracker dropped is back to 1.0: say so once, then forget it.
		for cell := range e.lastSurgePublished {
			if _, active := snap[cell]; !active {
				surgeEvents = append(surgeEvents, surgePublish{cell: cell.String(), multiplier: 1})
				delete(e.lastSurgePublished, cell)
			}
		}
		var frac float64
		if len(snap) > 0 {
			frac = float64(hot) / float64(len(snap))
		}
		promexport.SetSurgeFraction(frac)
	}

	publishTime := e.currentTime
	e.currentTime = e.currentTime.Add(dt)
	e.tickCount++

	e.mu.Unlock()

	e.outbox.flush()
	// Keys keep each driver's and each cell's updates in order on one partition.
	for _, d := range driverEvents {
		meta := e.stamper.MetaFor(publishTime)
		meta.PartitionKey = fmt.Sprintf("driver:%d", d.id)
		_ = events.PublishDriverLocationUpdate(e.bus, &eventspb.DriverLocationUpdate{
			Meta:     meta,
			DriverId: int64(d.id),
			Lat:      d.lat,
			Lon:      d.lon,
			State:    d.state,
		})
	}
	for _, s := range surgeEvents {
		meta := e.stamper.MetaFor(publishTime)
		meta.PartitionKey = "cell:" + s.cell
		_ = events.PublishSurgeUpdated(e.bus, &eventspb.SurgeUpdated{
			Meta:       meta,
			H3Cell:     s.cell,
			Multiplier: s.multiplier,
		})
	}
}

// Run drives the simulation. State transitions and speed changes are owned
// here; external callers send commands via Pause/Resume/Stop/SetSpeed.
func (e *Engine) Run() {
	if !e.state.CompareAndSwap(int32(StateStopped), int32(StateRunning)) {
		return
	}
	defer e.state.Store(int32(StateStopped))

	ticker := time.NewTicker(time.Duration(float64(time.Second) / e.tickRate))
	defer ticker.Stop()

	for {
		if SimulationState(e.state.Load()) == StatePaused {
			select {
			case <-e.stop:
				return
			case c := <-e.cmds:
				e.handleCmd(c)
			}
			continue
		}
		select {
		case <-e.stop:
			return
		case c := <-e.cmds:
			e.handleCmd(c)
		case <-ticker.C:
			e.Tick()
		}
	}
}

func (e *Engine) handleCmd(c command) {
	switch c.kind {
	case cmdPause:
		e.state.Store(int32(StatePaused))
	case cmdResume:
		e.state.Store(int32(StateRunning))
	case cmdSetSpeed:
		e.mu.Lock()
		e.speedMultiplier = c.arg
		e.mu.Unlock()
	}
}

// sendCmd drops the command when the buffer is full; losing a UI click is fine.
func (e *Engine) sendCmd(c command) {
	select {
	case e.cmds <- c:
	default:
	}
}

// Stop is idempotent and always reaches Run, even when the command channel is full.
func (e *Engine) Stop() {
	e.stopOnce.Do(func() { close(e.stop) })
}

func (e *Engine) Pause()  { e.sendCmd(command{kind: cmdPause}) }
func (e *Engine) Resume() { e.sendCmd(command{kind: cmdResume}) }

// SetSpeed changes the sim-speed multiplier. A value that isn't finite and
// positive is ignored: zero would freeze the clock and a negative one would run
// it backwards.
func (e *Engine) SetSpeed(multiplier float64) {
	if validSpeed(multiplier) {
		e.sendCmd(command{kind: cmdSetSpeed, arg: multiplier})
	}
}

func validSpeed(x float64) bool { return x > 0 && !math.IsInf(x, 1) }

func (e *Engine) GetState() SimulationState {
	return SimulationState(e.state.Load())
}

func (e *Engine) GetCurrentTime() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentTime
}

func (e *Engine) GetTickCount() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tickCount
}

func (e *Engine) GetSpeed() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.speedMultiplier
}

// GetVehicles returns live vehicle pointers. Read their fields only between
// Ticks, or use GetVehicleSnapshots.
func (e *Engine) GetVehicles() map[int]*agent.Vehicle {
	e.mu.RLock()
	defer e.mu.RUnlock()

	vehicles := make(map[int]*agent.Vehicle, len(e.vehicles))
	for id, v := range e.vehicles {
		vehicles[id] = v
	}
	return vehicles
}

// GetVehicleSnapshots returns value-copies of every vehicle, taken atomically
// under the engine lock. Use this from goroutines that race with Tick.
func (e *Engine) GetVehicleSnapshots() []agent.VehicleSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snaps := make([]agent.VehicleSnapshot, 0, len(e.vehicles))
	for _, v := range e.vehicles {
		if snap, ok := v.Snapshot(); ok {
			snaps = append(snaps, snap)
		}
	}
	return snaps
}

func (e *Engine) GetTrafficModel() *traffic.TrafficModel         { return e.trafficModel }
func (e *Engine) GetDispatcher() *dispatcher.Dispatcher          { return e.dispatcher }
func (e *Engine) GetMetricsCollector() *metrics.MetricsCollector { return e.metricsCollector }
func (e *Engine) GetGraph() *graph.Graph                         { return e.graph }
