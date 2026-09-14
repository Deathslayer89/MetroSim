// Package server serves the in-process simulation: the static frontend, a
// websocket stream with pause, resume and speed controls, and /metrics.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/simulation"
	"github.com/Deathslayer89/MetroSim/internal/wshub"
)

var droppedFrames = promauto.NewCounter(prometheus.CounterOpts{
	Name: "metrosim_server_dropped_frames_total",
	Help: "Websocket frames dropped because a client's send buffer was full.",
})

type VehicleUpdate struct {
	ID    int     `json:"id"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	State string  `json:"state"`
	Node  int     `json:"node"`
}

// EdgeCongestion carries its endpoint coordinates so the frontend can draw it
// without looking up nodes.
type EdgeCongestion struct {
	EdgeID   int     `json:"edge_id"`
	FromLat  float64 `json:"from_lat"`
	FromLon  float64 `json:"from_lon"`
	ToLat    float64 `json:"to_lat"`
	ToLon    float64 `json:"to_lon"`
	Density  int     `json:"density"`
	Capacity float64 `json:"capacity"`
	Factor   float64 `json:"factor"`
}

type MetricsUpdate struct {
	TotalRides     int     `json:"total_rides"`
	ActiveRides    int     `json:"active_rides"`
	PendingReqs    int     `json:"pending_requests"`
	Abandoned      int     `json:"abandoned"`
	AvgWaitTime    float64 `json:"avg_wait_time"`
	P95WaitTime    float64 `json:"p95_wait_time"`
	Throughput     float64 `json:"throughput"`
	AvgCongestion  float64 `json:"avg_congestion"`
	MaxCongestion  float64 `json:"max_congestion"`
	CongestedEdges int     `json:"congested_edges"`
}

// H3CellOverlay is a cell with idle drivers. Boundary holds [lat, lon] pairs,
// the order Leaflet expects.
type H3CellOverlay struct {
	Cell        string      `json:"cell"`
	Boundary    [][]float64 `json:"boundary"`
	DriverCount int         `json:"driver_count"`
}

// SurgeCellOverlay is a surge cell with its smoothed multiplier.
type SurgeCellOverlay struct {
	Cell       string      `json:"cell"`
	Boundary   [][]float64 `json:"boundary"`
	Multiplier float64     `json:"multiplier"`
}

// SimulationUpdate is one websocket frame; Type is "initial" on the first.
type SimulationUpdate struct {
	Type       string             `json:"type"`
	Tick       int64              `json:"tick"`
	Time       string             `json:"time"`
	Vehicles   []VehicleUpdate    `json:"vehicles,omitempty"`
	Congestion []EdgeCongestion   `json:"congestion,omitempty"`
	Hexes      []H3CellOverlay    `json:"hexes,omitempty"`
	Surge      []SurgeCellOverlay `json:"surge,omitempty"`
	Metrics    *MetricsUpdate     `json:"metrics,omitempty"`
	Paused     bool               `json:"paused"`
	Speed      float64            `json:"speed"`
}

type ControlRequest struct {
	Action string  `json:"action"` // pause, resume, stop or speed
	Value  float64 `json:"value,omitempty"`
}

type Server struct {
	engine     *simulation.Engine
	hub        *wshub.Hub
	httpServer *http.Server
	cancel     context.CancelFunc
}

// NewServer serves engine and feeds the engine's metrics collector, which the
// dashboard's ride stats come from, from its bus.
func NewServer(engine *simulation.Engine) *Server {
	engine.GetMetricsCollector().SubscribeToBus(engine.Bus())
	return &Server{engine: engine, hub: wshub.New(droppedFrames)}
}

// Start serves until Close is called or ListenAndServe fails.
func (s *Server) Start(addr string) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("web/static")))
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.Handle("/metrics", promhttp.Handler())

	s.httpServer = &http.Server{Addr: addr, Handler: mux}
	go s.broadcast(ctx)

	log.Printf("serving on %s", addr)
	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Close stops the broadcaster and shuts the HTTP server down gracefully.
func (s *Server) Close(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	u := s.buildSimulationUpdate()
	u.Type = "initial"
	first, err := json.Marshal(u)
	if err != nil {
		first = nil
	}
	if err := s.hub.Serve(w, r, first, s.handleControl); err != nil {
		log.Printf("websocket: %v", err)
	}
}

func (s *Server) handleControl(msg []byte) {
	var req ControlRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return
	}
	switch req.Action {
	case "pause":
		s.engine.Pause()
	case "resume":
		s.engine.Resume()
	case "stop":
		s.engine.Stop()
	case "speed":
		s.engine.SetSpeed(req.Value)
	}
}

func (s *Server) buildSimulationUpdate() SimulationUpdate {
	snapshots := s.engine.GetVehicleSnapshots()
	trafficModel := s.engine.GetTrafficModel()
	d := s.engine.GetDispatcher()
	metricsCollector := s.engine.GetMetricsCollector()

	// Drivers on their way to a pickup show as "assigned".
	assigned := d.AssignedDriverIDs()
	vehicleUpdates := make([]VehicleUpdate, 0, len(snapshots))
	for _, snap := range snapshots {
		state := snap.State.String()
		if assigned[snap.ID] {
			state = "assigned"
		}
		vehicleUpdates = append(vehicleUpdates, VehicleUpdate{
			ID:    snap.ID,
			Lat:   snap.Lat,
			Lon:   snap.Lon,
			State: state,
			Node:  snap.CurrentNode,
		})
	}

	// BPR gives every occupied edge a factor above 1; skip the barely-slowed ones.
	congestionData := make([]EdgeCongestion, 0)
	g := s.engine.GetGraph()
	for edgeID := range trafficModel.GetEdgeWeights() {
		factor := trafficModel.GetCongestionFactor(edgeID)
		if factor <= 1.05 {
			continue
		}
		edge, err := g.GetEdge(edgeID)
		if err != nil {
			continue
		}
		from, err := g.GetNode(edge.FromNode)
		if err != nil {
			continue
		}
		to, err := g.GetNode(edge.ToNode)
		if err != nil {
			continue
		}
		congestionData = append(congestionData, EdgeCongestion{
			EdgeID:   edgeID,
			FromLat:  from.Lat,
			FromLon:  from.Lon,
			ToLat:    to.Lat,
			ToLon:    to.Lon,
			Density:  trafficModel.GetEdgeDensity(edgeID),
			Capacity: trafficModel.GetEdgeCapacity(edgeID),
			Factor:   factor,
		})
	}

	stats := metricsCollector.GetStats()
	trafficStats := trafficModel.GetStats()

	metrics := &MetricsUpdate{
		TotalRides:     stats.TotalRides,
		ActiveRides:    d.GetActiveRideCount(),
		PendingReqs:    d.GetPendingCount(),
		Abandoned:      d.AbandonedCount(),
		AvgWaitTime:    stats.AvgWaitTime,
		P95WaitTime:    stats.P95WaitTime,
		Throughput:     stats.Throughput,
		AvgCongestion:  trafficStats.AvgCongestion,
		MaxCongestion:  trafficStats.MaxCongestion,
		CongestedEdges: trafficStats.CongestedEdges,
	}

	return SimulationUpdate{
		Type:       "update",
		Tick:       s.engine.GetTickCount(),
		Time:       s.engine.GetCurrentTime().Format(time.RFC3339),
		Vehicles:   vehicleUpdates,
		Congestion: congestionData,
		Hexes:      buildHexOverlay(d),
		Surge:      buildSurgeOverlay(s.engine),
		Metrics:    metrics,
		Paused:     s.engine.GetState() == simulation.StatePaused,
		Speed:      s.engine.GetSpeed(),
	}
}

func buildSurgeOverlay(engine *simulation.Engine) []SurgeCellOverlay {
	tracker := engine.GetSurge()
	if tracker == nil {
		return nil
	}
	cells := tracker.Snapshot()
	out := make([]SurgeCellOverlay, 0, len(cells))
	for cell, mult := range cells {
		pts, ok := cellBoundary(cell)
		if !ok {
			continue
		}
		out = append(out, SurgeCellOverlay{
			Cell:       cell.String(),
			Boundary:   pts,
			Multiplier: mult,
		})
	}
	return out
}

func buildHexOverlay(d *dispatcher.Dispatcher) []H3CellOverlay {
	counts := d.DriverCells()
	out := make([]H3CellOverlay, 0, len(counts))
	for cell, n := range counts {
		pts, ok := cellBoundary(cell)
		if !ok {
			continue
		}
		out = append(out, H3CellOverlay{
			Cell:        cell.String(),
			Boundary:    pts,
			DriverCount: n,
		})
	}
	return out
}

// cellBoundary returns a cell's outline as [lat, lon] pairs.
func cellBoundary(cell h3.Cell) ([][]float64, bool) {
	boundary, err := cell.Boundary()
	if err != nil {
		return nil, false
	}
	pts := make([][]float64, 0, len(boundary))
	for _, ll := range boundary {
		pts = append(pts, []float64{ll.Lat, ll.Lng})
	}
	return pts, true
}

// broadcast pushes a snapshot every 100 ms until ctx is cancelled.
func (s *Server) broadcast(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		payload, err := json.Marshal(s.buildSimulationUpdate())
		if err != nil {
			continue
		}
		s.hub.Broadcast(payload)
	}
}
