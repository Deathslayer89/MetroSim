package main

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
)

// A rider still aboard when the run stops was picked up, so their wait is known
// and has to count. Only riders never picked up have no wait.
func TestRunHeadlessCountsRidersStillAboard(t *testing.T) {
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	sc := &scenario.Scenario{
		Name:      "aboard",
		Duration:  20 * time.Second,
		Seed:      1,
		StartTime: time.Unix(0, 0),
		Vehicles:  scenario.VehicleConfig{Count: 3, Spawn: "random"},
		Arrivals: scenario.ArrivalConfig{RateSegments: []scenario.RatePoint{
			{T: 0, Rate: 1}, {T: 20 * time.Second, Rate: 1},
		}},
	}
	res, err := runHeadless(g, sc, "greedy", 3*time.Second, 0, sc.Seed, "", dispatcher.DriverBehavior{}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.waits) <= res.completed {
		t.Errorf("want waits for riders still aboard too: %d waits, %d completed trips", len(res.waits), res.completed)
	}
}
