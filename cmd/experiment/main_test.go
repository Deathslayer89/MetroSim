package main

import (
	"slices"
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

// Each demand level scales its own copy of the arrival rates, so the loaded
// scenario every level starts from stays as it was.
func TestScaledCopiesTheArrivalRates(t *testing.T) {
	sc := steady("base", 1, 0.5)
	two := scaled(sc, 2)
	if got := two.Arrivals.RateSegments[1].Rate; got != 1 {
		t.Errorf("at 2x the rate is %v, want 1", got)
	}
	if got := sc.Arrivals.RateSegments[1].Rate; got != 0.5 {
		t.Errorf("scaling changed the loaded scenario's rate to %v", got)
	}
	if two.Name == sc.Name {
		t.Error("the scaled scenario kept its name, so its traces would overwrite the original's")
	}
}

func TestParseDemandsSortsAndRejectsBadMultipliers(t *testing.T) {
	got, err := parseDemands("2, 1,1.5")
	if err != nil || !slices.Equal(got, []float64{1, 1.5, 2}) {
		t.Errorf(`parseDemands("2, 1,1.5") = %v, %v`, got, err)
	}
	for _, bad := range []string{"0", "-1", "x", "NaN", "Inf", "1,1"} {
		if _, err := parseDemands(bad); err == nil {
			t.Errorf("parseDemands(%q) accepted it", bad)
		}
	}
}
