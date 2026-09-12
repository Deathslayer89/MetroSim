package dispatcher

import (
	"math"
	"sort"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
)

// MatchCtx is what a Policy sees for one Match call. Dispatcher.Tick builds it
// under the dispatcher lock.
type MatchCtx struct {
	Pending     []*Request
	DriverIndex *H3DriverIndex
	Vehicles    map[int]*agent.Vehicle
	EdgeWeights map[int]float64 // live congestion-adjusted weights; nil = use BaseWeight
	Graph       *graph.Graph
	PathPlanner *pathfinding.PathPlanner
	Now         time.Time
	// SurgeAt gives the surge multiplier at a pickup; nil means 1.0 everywhere.
	SurgeAt func(lat, lon float64) float64
	// ETAEstimate, when set, replaces per-candidate A* in the cost matrix. The
	// live sim needs it to keep up on the city graph.
	ETAEstimate func(fromNode, toNode int) float64
	// TripETA predicts a trip's pickup-to-dropoff duration, or 0 without a model.
	TripETA func(pickupNode, dropoffNode int) float64
}

// Assignment pairs a request with a driver. Cost, in seconds, is what the
// policy minimized.
type Assignment struct {
	Request  *Request
	DriverID int
	Cost     float64
}

// Policy decides which idle drivers take which pending requests. Match is never
// called concurrently.
type Policy interface {
	Name() string
	Match(ctx MatchCtx) []Assignment
}

// computeETA returns the routed seconds from driver's position to req's pickup
// using live edge weights when present. Returns +Inf if unreachable. A car
// partway along an edge has to finish it first, so the time left on it counts.
func computeETA(ctx MatchCtx, driver *agent.Vehicle, req *Request) float64 {
	from, lead := driver.CurrentNode, 0.0
	if e := driver.CurrentEdge; e != nil {
		from = e.ToNode
		lead = (1 - driver.Progress) * edgeTime(ctx, e)
	}
	if from == req.PickupNode {
		return lead
	}
	if ctx.ETAEstimate != nil {
		return lead + ctx.ETAEstimate(from, req.PickupNode)
	}
	var path []*graph.Edge
	var err error
	if ctx.EdgeWeights != nil {
		path, err = ctx.PathPlanner.FindPathWithWeights(from, req.PickupNode, ctx.EdgeWeights)
	} else {
		path, err = ctx.PathPlanner.FindPath(from, req.PickupNode)
	}
	if err != nil {
		return math.Inf(1)
	}
	eta := lead
	for _, edge := range path {
		eta += edgeTime(ctx, edge)
	}
	return eta
}

// edgeTime is an edge's live travel time, or free flow when it isn't congested.
func edgeTime(ctx MatchCtx, e *graph.Edge) float64 {
	if w, ok := ctx.EdgeWeights[e.ID]; ok {
		return w
	}
	return e.BaseWeight
}

// GreedyPolicy takes requests in arrival order and gives each the nearest idle
// driver that can reach it.
type GreedyPolicy struct{}

func (GreedyPolicy) Name() string { return "greedy" }

func (GreedyPolicy) Match(ctx MatchCtx) []Assignment {
	if len(ctx.Pending) == 0 {
		return nil
	}
	// Consider the nearest few, not just the single closest, so an unreachable
	// nearest driver doesn't strand a request that other drivers could serve.
	const greedyCandidates = 5
	out := make([]Assignment, 0, len(ctx.Pending))
	for _, req := range ctx.Pending {
		pickup, err := ctx.Graph.GetNode(req.PickupNode)
		if err != nil {
			continue
		}
		for _, c := range ctx.DriverIndex.NearestK(pickup.Lat, pickup.Lon, greedyCandidates) {
			driver, ok := ctx.Vehicles[c.ID]
			if !ok || !driver.IsAvailable() {
				continue
			}
			cost := computeETA(ctx, driver, req)
			if math.IsInf(cost, 1) {
				continue // unreachable; try the next-nearest
			}
			out = append(out, Assignment{Request: req, DriverID: driver.ID, Cost: cost})
			ctx.DriverIndex.Remove(driver.ID)
			break
		}
	}
	return out
}

// BatchPolicy collects requests for Window, then solves one assignment over
// every pending request and its nearest CandidatesPerRequest drivers,
// minimizing total pickup ETA. SurgePremiumSeconds discounts high-surge pickups
// and ETAWeight penalizes long predicted trips; both default to 0. Match
// updates lastFire, so each Dispatcher needs its own BatchPolicy.
type BatchPolicy struct {
	Window               time.Duration
	CandidatesPerRequest int
	SurgePremiumSeconds  float64
	ETAWeight            float64
	lastFire             time.Time
}

func NewBatchPolicy(window time.Duration) *BatchPolicy {
	return &BatchPolicy{Window: window, CandidatesPerRequest: 5}
}

func (b *BatchPolicy) Name() string { return "batch" }

func (b *BatchPolicy) Match(ctx MatchCtx) []Assignment {
	if !b.lastFire.IsZero() && ctx.Now.Sub(b.lastFire) < b.Window {
		return nil
	}
	b.lastFire = ctx.Now
	return matchBatch(ctx, ctx.Pending, b.CandidatesPerRequest, b.SurgePremiumSeconds, b.ETAWeight)
}

// matchBatch solves one assignment over pending and their NearestK candidates.
// It leaves ctx.DriverIndex alone; RegionShardedBatchPolicy removes assigned
// drivers itself between regions.
func matchBatch(ctx MatchCtx, pending []*Request, candidatesPerRequest int, surgePremium, etaWeight float64) []Assignment {
	if len(pending) == 0 {
		return nil
	}

	perReq := make([][]int, len(pending))
	driverSet := make(map[int]struct{})
	for i, req := range pending {
		pickup, err := ctx.Graph.GetNode(req.PickupNode)
		if err != nil {
			continue
		}
		k := candidatesPerRequest
		if k <= 0 {
			k = 5
		}
		nearest := ctx.DriverIndex.NearestK(pickup.Lat, pickup.Lon, k)
		ids := make([]int, 0, len(nearest))
		for _, n := range nearest {
			if v, ok := ctx.Vehicles[n.ID]; ok && v.IsAvailable() {
				ids = append(ids, n.ID)
				driverSet[n.ID] = struct{}{}
			}
		}
		perReq[i] = ids
	}

	drivers := make([]int, 0, len(driverSet))
	for id := range driverSet {
		drivers = append(drivers, id)
	}
	sort.Ints(drivers)
	if len(drivers) == 0 {
		return nil
	}
	driverIdx := make(map[int]int, len(drivers))
	for i, d := range drivers {
		driverIdx[d] = i
	}

	const unreachable = 1e9
	R, D := len(pending), len(drivers)
	N := R
	if D > N {
		N = D
	}
	cost := make([][]float64, N)
	for i := range cost {
		cost[i] = make([]float64, N)
		for j := range cost[i] {
			cost[i][j] = unreachable
		}
	}

	for i, req := range pending {
		// The surge discount and the duration bias are the same for every
		// candidate of a request, so they decide which requests win contested
		// drivers, never which driver a request gets. Costs can go negative.
		var surgePenalty float64
		if ctx.SurgeAt != nil && surgePremium > 0 {
			pickupNode, err := ctx.Graph.GetNode(req.PickupNode)
			if err == nil {
				surgePenalty = surgePremium * (ctx.SurgeAt(pickupNode.Lat, pickupNode.Lon) - 1.0)
			}
		}
		var durBias float64
		if etaWeight > 0 && ctx.TripETA != nil {
			durBias = etaWeight * ctx.TripETA(req.PickupNode, req.DestinationNode)
		}
		for _, did := range perReq[i] {
			eta := computeETA(ctx, ctx.Vehicles[did], req)
			if math.IsInf(eta, 1) {
				continue
			}
			cost[i][driverIdx[did]] = eta - surgePenalty + durBias
		}
	}

	assign := hungarianMinCost(cost)

	out := make([]Assignment, 0, R)
	for i := 0; i < R; i++ {
		j := assign[i]
		if j >= D || cost[i][j] >= unreachable {
			continue
		}
		out = append(out, Assignment{
			Request:  pending[i],
			DriverID: drivers[j],
			Cost:     cost[i][j],
		})
	}
	return out
}
