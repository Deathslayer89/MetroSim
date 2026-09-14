package dispatcher

import (
	"sort"
	"time"

	"github.com/uber/h3-go/v4"
)

// RegionShardedBatchPolicy runs a separate solve per pickup H3 region, about
// 1/k^2 the cost of one solve over k regions. Regions go in sorted order and
// remove the drivers they assign, so no boundary driver is claimed twice.
type RegionShardedBatchPolicy struct {
	Window               time.Duration
	CandidatesPerRequest int
	SurgePremiumSeconds  float64
	ETAWeight            float64
	Resolution           int // H3 resolution of a region
	lastFire             time.Time
}

// NewRegionShardedBatchPolicy returns a sharded matcher at H3 resolution 5.
func NewRegionShardedBatchPolicy(window time.Duration) *RegionShardedBatchPolicy {
	return &RegionShardedBatchPolicy{Window: window, CandidatesPerRequest: 5, Resolution: 5}
}

func (p *RegionShardedBatchPolicy) Match(ctx MatchCtx) []Assignment {
	if !p.lastFire.IsZero() && ctx.Now.Sub(p.lastFire) < p.Window {
		return nil
	}
	p.lastFire = ctx.Now
	if len(ctx.Pending) == 0 {
		return nil
	}

	res := p.Resolution
	if res <= 0 {
		res = 5
	}

	// Pickups with no H3 cell share region 0.
	groups := make(map[h3.Cell][]*Request)
	for _, req := range ctx.Pending {
		var cell h3.Cell
		if n, err := ctx.Graph.GetNode(req.PickupNode); err == nil {
			if c, err := h3.LatLngToCell(h3.LatLng{Lat: n.Lat, Lng: n.Lon}, res); err == nil {
				cell = c
			}
		}
		groups[cell] = append(groups[cell], req)
	}

	// Sorted region order keeps the boundary-driver tie-break reproducible.
	cells := make([]h3.Cell, 0, len(groups))
	for c := range groups {
		cells = append(cells, c)
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i] < cells[j] })

	out := make([]Assignment, 0, len(ctx.Pending))
	for _, c := range cells {
		regionAssignments := matchBatch(ctx, groups[c], p.CandidatesPerRequest, p.SurgePremiumSeconds, p.ETAWeight)
		for _, a := range regionAssignments {
			out = append(out, a)
			ctx.DriverIndex.Remove(a.DriverID)
		}
	}
	return out
}
