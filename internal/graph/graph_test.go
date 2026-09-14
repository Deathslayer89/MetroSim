package graph

import (
	"math"
	"testing"
)

func TestEuclideanDistanceAppliesCosLat(t *testing.T) {
	// A degree of longitude shrinks by cos(latitude): at 60 degrees it's half
	// its length at the equator.
	eq := EuclideanDistance(&Node{Lat: 0, Lon: 0}, &Node{Lat: 0, Lon: 1})
	hi := EuclideanDistance(&Node{Lat: 60, Lon: 0}, &Node{Lat: 60, Lon: 1})
	ratio := hi / eq
	if math.Abs(ratio-0.5) > 0.02 {
		t.Errorf("a longitude degree at 60 should be ~0.5x the equator's, got %.3fx", ratio)
	}
	// One degree of latitude is ~111 km regardless of longitude.
	lat := EuclideanDistance(&Node{Lat: 37, Lon: -122}, &Node{Lat: 38, Lon: -122})
	if math.Abs(lat-111000) > 1000 {
		t.Errorf("a degree of latitude should be ~111000 m, got %.0f", lat)
	}
}

func TestAddEdgeDerivesBaseWeight(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: 0})
	g.AddNode(&Node{ID: 1})
	g.AddEdge(&Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 100, SpeedLimit: 10})
	if w := g.GetEdgeWeight(0); w != 10 {
		t.Errorf("BaseWeight should be length/speed = 10, got %v", w)
	}
}

func TestAddEdgeGuardsZeroSpeed(t *testing.T) {
	g := NewGraph()
	g.AddNode(&Node{ID: 0})
	g.AddNode(&Node{ID: 1})
	g.AddEdge(&Edge{ID: 0, FromNode: 0, ToNode: 1, Length: 100, SpeedLimit: 0})
	if w := g.GetEdgeWeight(0); !math.IsInf(w, 1) {
		t.Errorf("zero-speed edge should get +Inf weight (unusable), got %v", w)
	}
}

func TestGetNodeAndEdgeBounds(t *testing.T) {
	g := NewGraph()
	if _, err := g.GetNode(42); err == nil {
		t.Error("GetNode on missing node should error")
	}
	if _, err := g.GetEdge(42); err == nil {
		t.Error("GetEdge on missing edge should error")
	}
	if w := g.GetEdgeWeight(42); !math.IsInf(w, 1) {
		t.Errorf("GetEdgeWeight on missing edge should be +Inf, got %v", w)
	}
}

// One degree of latitude is the Earth's radius times pi/180.
func TestHaversineMetersOneDegree(t *testing.T) {
	if got, want := HaversineMeters(0, 0, 1, 0), 6371000*math.Pi/180; math.Abs(got-want) > 1e-6 {
		t.Errorf("one degree of latitude: want %.3f m, got %.3f m", want, got)
	}
}
