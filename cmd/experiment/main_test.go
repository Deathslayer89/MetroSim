package main

import (
	"testing"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
)

func loadGrid(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	return g
}

// steady asks for rate rides a second for 20 s from cars cars.
func steady(name string, cars int, rate float64) *scenario.Scenario {
	return &scenario.Scenario{
		Name:      name,
		Duration:  20 * time.Second,
		Seed:      1,
		StartTime: time.Unix(0, 0),
		Vehicles:  scenario.VehicleConfig{Count: cars, Spawn: "random"},
		Arrivals: scenario.ArrivalConfig{RateSegments: []scenario.RatePoint{
			{T: 0, Rate: rate}, {T: 20 * time.Second, Rate: rate},
		}},
	}
}

// A rider still aboard when the run stops was picked up, so their wait is known
// and has to count.
func TestRunHeadlessCountsRidersStillAboard(t *testing.T) {
	sc := steady("aboard", 3, 1)
	res, err := runHeadless(loadGrid(t), sc, "greedy", 3*time.Second, 0, sc.Seed, "", dispatcher.DriverBehavior{}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.pickedUp <= res.completed {
		t.Errorf("want pickups of riders still aboard too: %d picked up, %d completed trips", res.pickedUp, res.completed)
	}
}

// A rider never picked up waited from their request to the end of the run, and
// has to count too, or a policy that strands riders looks faster than one that
// picks them up late.
func TestRunHeadlessCountsRidersNeverPickedUp(t *testing.T) {
	sc := steady("stranded", 1, 1)
	res, err := runHeadless(loadGrid(t), sc, "greedy", 3*time.Second, 0, sc.Seed, "", dispatcher.DriverBehavior{}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.pickedUp == res.requested {
		t.Fatal("one car picked up every rider; the test needs some left waiting")
	}
	if len(res.waits) != res.requested {
		t.Errorf("%d waits for %d riders", len(res.waits), res.requested)
	}
}
