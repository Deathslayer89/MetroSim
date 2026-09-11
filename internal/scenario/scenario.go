// Package scenario loads simulation configurations from YAML and drives
// deterministic Poisson request generation over a graph.
package scenario

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Scenario struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description,omitempty"`
	Duration    time.Duration `yaml:"duration"`
	Seed        int64         `yaml:"seed"`
	StartTime   time.Time     `yaml:"start_time,omitempty"` // sim time of tick 0; zero means time.Now()
	Vehicles    VehicleConfig `yaml:"vehicles"`
	Arrivals    ArrivalConfig `yaml:"arrivals"`
}

type VehicleConfig struct {
	Count int    `yaml:"count"`
	Spawn string `yaml:"spawn"` // "random" or "pickups"
}

type ArrivalConfig struct {
	RateSegments    []RatePoint `yaml:"rate_segments"`
	PickupHotspots  []Hotspot   `yaml:"pickup_hotspots,omitempty"`
	DropoffHotspots []Hotspot   `yaml:"dropoff_hotspots,omitempty"`
}

type RatePoint struct {
	T    time.Duration `yaml:"t"`    // seconds since sim start
	Rate float64       `yaml:"rate"` // requests per second at this point
}

// Hotspot concentrates demand: weight w draws w times as many samples as the
// uniform background spread over the whole map.
type Hotspot struct {
	Name    string  `yaml:"name,omitempty"`
	Lat     float64 `yaml:"lat"`
	Lon     float64 `yaml:"lon"`
	RadiusM float64 `yaml:"radius_m"`
	Weight  float64 `yaml:"weight"`
}

func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("invalid scenario %s: %w", path, err)
	}
	return &s, nil
}

func (s *Scenario) validate() error {
	if s.Name == "" {
		return fmt.Errorf("name required")
	}
	if s.Duration <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	if s.Vehicles.Count <= 0 {
		return fmt.Errorf("vehicles.count must be positive")
	}
	switch s.Vehicles.Spawn {
	case "":
		s.Vehicles.Spawn = "random"
	case "random", "pickups":
	default:
		return fmt.Errorf("vehicles.spawn %q not supported (want random or pickups)", s.Vehicles.Spawn)
	}
	if len(s.Arrivals.RateSegments) == 0 {
		return fmt.Errorf("arrivals.rate_segments must have at least one entry")
	}
	for i := 1; i < len(s.Arrivals.RateSegments); i++ {
		if s.Arrivals.RateSegments[i].T <= s.Arrivals.RateSegments[i-1].T {
			return fmt.Errorf("arrivals.rate_segments must be strictly increasing in t")
		}
	}
	for _, seg := range s.Arrivals.RateSegments {
		if seg.Rate < 0 {
			return fmt.Errorf("arrivals.rate_segments rate must be non-negative, got %g", seg.Rate)
		}
	}
	for _, h := range s.Arrivals.PickupHotspots {
		if err := h.validate(); err != nil {
			return fmt.Errorf("pickup_hotspots: %w", err)
		}
	}
	for _, h := range s.Arrivals.DropoffHotspots {
		if err := h.validate(); err != nil {
			return fmt.Errorf("dropoff_hotspots: %w", err)
		}
	}
	return nil
}

func (h Hotspot) validate() error {
	if h.RadiusM <= 0 || h.Weight <= 0 {
		return fmt.Errorf("radius_m and weight must be positive")
	}
	if h.Lat < -90 || h.Lat > 90 || h.Lon < -180 || h.Lon > 180 {
		return fmt.Errorf("lat/lon out of range (%g, %g)", h.Lat, h.Lon)
	}
	return nil
}
