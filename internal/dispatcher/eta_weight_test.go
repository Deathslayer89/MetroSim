package dispatcher

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/agent"
	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// One driver, two requests. At weight 0 the driver takes the closer pickup; at
// weight 1 the predicted trip length dominates and it takes the short trip,
// which frees it sooner, even though that pickup is farther away.
func TestBatchETAWeightBiasesTowardShortTrips(t *testing.T) {
	g := graph.NewGraph()
	g.AddNode(&graph.Node{ID: 0, Lat: 37.7700, Lon: -122.4200}) // driver
	g.AddNode(&graph.Node{ID: 1, Lat: 37.7701, Lon: -122.4200}) // pickup A
	g.AddNode(&graph.Node{ID: 2, Lat: 37.7702, Lon: -122.4200}) // dropoff A (short trip)
	g.AddNode(&graph.Node{ID: 3, Lat: 37.7703, Lon: -122.4200}) // pickup B
	g.AddNode(&graph.Node{ID: 4, Lat: 37.9000, Lon: -122.4200}) // dropoff B, far away

	drv := agent.NewVehicle(1, 0, g, nil)
	vehicles := map[int]*agent.Vehicle{1: drv}
	idx := NewH3DriverIndex()
	lat, lon, _ := drv.GetPosition()
	idx.Insert(1, lat, lon)

	reqA := &Request{ID: 1, PickupNode: 1, DestinationNode: 2}
	reqB := &Request{ID: 2, PickupNode: 3, DestinationNode: 4}

	ctx := MatchCtx{
		Pending:     []*Request{reqA, reqB},
		DriverIndex: idx,
		Vehicles:    vehicles,
		Graph:       g,
		Now:         time.Unix(0, 0),
		ETAEstimate: func(_, pickup int) float64 { // B's pickup is closer
			switch pickup {
			case 1:
				return 100 // to pickup A
			case 3:
				return 50 // to pickup B
			}
			return 0
		},
		TripETA: func(_, dropoff int) float64 { // A is a short trip, B is long
			switch dropoff {
			case 2:
				return 60
			case 4:
				return 600
			}
			return 0
		},
	}

	pickFor := func(assignments []Assignment) int {
		if len(assignments) != 1 {
			t.Fatalf("want exactly 1 assignment, got %d", len(assignments))
		}
		return assignments[0].Request.ID
	}

	if got := pickFor(matchBatch(ctx, ctx.Pending, 5, 0, 0)); got != reqB.ID {
		t.Errorf("weight 0: expected the closer-pickup request B (id %d), got id %d", reqB.ID, got)
	}
	if got := pickFor(matchBatch(ctx, ctx.Pending, 5, 0, 1.0)); got != reqA.ID {
		t.Errorf("weight 1: expected the short-trip request A (id %d), got id %d", reqA.ID, got)
	}
}
