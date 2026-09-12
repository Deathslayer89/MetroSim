// Package tracelog writes one Parquet row per completed trip.
package tracelog

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// SubscribeToBus records TripCompleted events under the "tracelog" group,
// logging and skipping any trip it fails to write. Over Kafka, RollingRecorder
// is the safer choice: it commits offsets only after its files are complete.
func (r *Recorder) SubscribeToBus(bus events.Bus) {
	_ = events.SubscribeTripCompleted(bus, "tracelog", func(c *eventspb.TripCompleted) {
		err := r.Record(TripInput{
			RideID:          c.RideId,
			RequestTs:       c.RequestTime.AsTime(),
			MatchTs:         c.MatchTime.AsTime(),
			PickupTs:        c.PickupTime.AsTime(),
			DropoffTs:       c.DropoffTime.AsTime(),
			PickupNode:      int(c.PickupNode),
			DropoffNode:     int(c.DropoffNode),
			PickupLat:       c.PickupLat,
			PickupLon:       c.PickupLon,
			DropoffLat:      c.DropoffLat,
			DropoffLon:      c.DropoffLon,
			SurgeAtMatch:    c.SurgeAtMatch,
			ETAPredictedSec: c.EtaPredictedS,
		})
		if err != nil {
			log.Printf("tracelog: record ride %d: %v", c.RideId, err)
		}
	})
}

// TripRow is the on-disk schema. Field tags are parquet column names.
type TripRow struct {
	RideID          int64   `parquet:"ride_id"`
	Scenario        string  `parquet:"scenario"`
	Policy          string  `parquet:"policy"`
	Seed            int64   `parquet:"seed"`
	RequestTsMs     int64   `parquet:"request_ts_unix_ms"`
	MatchTsMs       int64   `parquet:"match_ts_unix_ms"`
	PickupTsMs      int64   `parquet:"pickup_ts_unix_ms"`
	DropoffTsMs     int64   `parquet:"dropoff_ts_unix_ms"`
	PickupNode      int64   `parquet:"pickup_node"`
	DropoffNode     int64   `parquet:"dropoff_node"`
	PickupH3R9      string  `parquet:"pickup_h3_r9"`
	DropoffH3R9     string  `parquet:"dropoff_h3_r9"`
	SurgeAtMatch    float64 `parquet:"surge_at_match"`
	ETAPredictedSec float64 `parquet:"eta_predicted_s"`
	ETAActualSec    float64 `parquet:"eta_actual_s"`
	WaitTimeSec     float64 `parquet:"wait_time_s"`
	TripDurationSec float64 `parquet:"trip_duration_s"`
}

// TripInput is one completed trip. Non-empty Scenario, Policy and Seed override
// the labels given to Open; trace-writer relies on that because one directory
// holds many runs.
type TripInput struct {
	RideID          int64
	Scenario        string
	Policy          string
	Seed            int64
	RequestTs       time.Time
	MatchTs         time.Time
	PickupTs        time.Time
	DropoffTs       time.Time
	PickupNode      int
	DropoffNode     int
	PickupLat       float64
	PickupLon       float64
	DropoffLat      float64
	DropoffLon      float64
	SurgeAtMatch    float64
	ETAPredictedSec float64
}

type Recorder struct {
	mu       sync.Mutex
	scenario string
	policy   string
	seed     int64
	f        *os.File
	w        *parquet.GenericWriter[TripRow]
}

// Open creates a Parquet file at path and stamps scenario, policy and seed on
// every row.
func Open(path, scenario, policy string, seed int64) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	return &Recorder{
		scenario: scenario,
		policy:   policy,
		seed:     seed,
		f:        f,
		w:        parquet.NewGenericWriter[TripRow](f),
	}, nil
}

func (r *Recorder) Record(t TripInput) error {
	scenario := t.Scenario
	if scenario == "" {
		scenario = r.scenario
	}
	policy := t.Policy
	if policy == "" {
		policy = r.policy
	}
	seed := t.Seed
	if seed == 0 {
		seed = r.seed
	}
	row := TripRow{
		RideID:          t.RideID,
		Scenario:        scenario,
		Policy:          policy,
		Seed:            seed,
		RequestTsMs:     t.RequestTs.UnixMilli(),
		MatchTsMs:       t.MatchTs.UnixMilli(),
		PickupTsMs:      t.PickupTs.UnixMilli(),
		DropoffTsMs:     t.DropoffTs.UnixMilli(),
		PickupNode:      int64(t.PickupNode),
		DropoffNode:     int64(t.DropoffNode),
		PickupH3R9:      h3String(t.PickupLat, t.PickupLon, 9),
		DropoffH3R9:     h3String(t.DropoffLat, t.DropoffLon, 9),
		SurgeAtMatch:    t.SurgeAtMatch,
		ETAPredictedSec: t.ETAPredictedSec,
		// Ground truth for eta_predicted_s: the model predicts trip duration.
		ETAActualSec:    t.DropoffTs.Sub(t.PickupTs).Seconds(),
		WaitTimeSec:     t.PickupTs.Sub(t.RequestTs).Seconds(),
		TripDurationSec: t.DropoffTs.Sub(t.PickupTs).Seconds(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.w.Write([]TripRow{row})
	return err
}

// Close writes the footer and syncs the file to disk before closing it.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.w.Close(); err != nil {
		r.f.Close()
		return err
	}
	if err := r.f.Sync(); err != nil {
		r.f.Close()
		return err
	}
	return r.f.Close()
}

func h3String(lat, lon float64, res int) string {
	cell, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lon}, res)
	if err != nil {
		return ""
	}
	return cell.String()
}
