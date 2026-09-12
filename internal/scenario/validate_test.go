package scenario

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `name: t
duration: 60s
seed: 1
vehicles:
  count: 5
arrivals:
  rate_segments:
    - { t: 0s, rate: 1 }
`

func loadYAML(t *testing.T, body string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	return err
}

func TestShippedScenariosLoad(t *testing.T) {
	paths, err := filepath.Glob("../../scenarios/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no scenarios found: %v", err)
	}
	for _, p := range paths {
		if _, err := Load(p); err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
	if err := loadYAML(t, validYAML); err != nil {
		t.Errorf("minimal scenario: %v", err)
	}
}

// A misspelled key would otherwise vanish and quietly change the demand.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	body := validYAML + "  pickup_hotspot:\n    - { lat: 37.77, lon: -122.42, radius_m: 500, weight: 2 }\n"
	if err := loadYAML(t, body); err == nil {
		t.Fatal("a misspelled pickup_hotspots key was accepted")
	}
}

// A NaN rate would spin the Poisson sampler forever, and an infinite one would
// be capped without a word.
func TestLoadRejectsNonFiniteRates(t *testing.T) {
	for _, rate := range []string{".nan", ".inf"} {
		body := strings.Replace(validYAML, "rate: 1", "rate: "+rate, 1)
		if err := loadYAML(t, body); err == nil {
			t.Errorf("rate %s was accepted", rate)
		}
	}
}

// A hotspot with no road nodes inside it, from a sign typo in its longitude
// say, would be dropped and the other hotspots would take its share.
func TestNewGeneratorRejectsAHotspotWithNoRoads(t *testing.T) {
	s := mkScenario(1)
	s.Arrivals.PickupHotspots = []Hotspot{{Name: "typo", Lat: 37.7749, Lon: 122.4194, RadiusM: 500, Weight: 2}}
	if _, err := NewGenerator(s, buildTinyGraph(t)); err == nil {
		t.Fatal("a hotspot covering no road nodes was accepted")
	}
}

// Knuth's method underflows past a mean of about 700, so a large mean has to
// come out right by another route.
func TestSamplePoissonLargeMean(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const mean = 5000.0
	const n = 200
	sum := 0
	for i := 0; i < n; i++ {
		sum += samplePoisson(mean, rng)
	}
	if got := float64(sum) / n; math.Abs(got-mean) > 0.02*mean {
		t.Errorf("sample mean %.0f, want about %.0f", got, mean)
	}
}
