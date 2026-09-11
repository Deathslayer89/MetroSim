package tracelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func TestRecorderRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trips.parquet")
	r, err := Open(path, "smoke", "greedy", 42)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t0 := time.Unix(1_700_000_000, 0)
	in := TripInput{
		RideID:       7,
		RequestTs:    t0,
		MatchTs:      t0.Add(2 * time.Second),
		PickupTs:     t0.Add(30 * time.Second),
		DropoffTs:    t0.Add(120 * time.Second),
		PickupNode:   3,
		DropoffNode:  9,
		PickupLat:    37.7749,
		PickupLon:    -122.4194,
		DropoffLat:   37.7900,
		DropoffLon:   -122.4050,
		SurgeAtMatch: 1.5,
	}
	if err := r.Record(in); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer f.Close()
	info, _ := f.Stat()

	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatalf("parquet open: %v", err)
	}
	reader := parquet.NewGenericReader[TripRow](pf)
	defer reader.Close()
	rows := make([]TripRow, 1)
	n, err := reader.Read(rows)
	if err != nil && err.Error() != "EOF" {
		t.Fatalf("read: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row, got %d", n)
	}

	got := rows[0]
	wantH3PrefixLen := 15 // 15-hex-digit H3 cell IDs at res 9
	if got.RideID != 7 || got.Scenario != "smoke" || got.Policy != "greedy" || got.Seed != 42 {
		t.Errorf("constants: %+v", got)
	}
	if got.RequestTsMs != t0.UnixMilli() {
		t.Errorf("request_ts_ms: want %d, got %d", t0.UnixMilli(), got.RequestTsMs)
	}
	if got.WaitTimeSec != 30 {
		t.Errorf("wait_time_s: want 30, got %v", got.WaitTimeSec)
	}
	if got.TripDurationSec != 90 {
		t.Errorf("trip_duration_s: want 90, got %v", got.TripDurationSec)
	}
	if got.ETAActualSec != 90 {
		t.Errorf("eta_actual_s (trip duration): want 90, got %v", got.ETAActualSec)
	}
	if got.SurgeAtMatch != 1.5 {
		t.Errorf("surge_at_match: want 1.5, got %v", got.SurgeAtMatch)
	}
	if len(got.PickupH3R9) != wantH3PrefixLen || len(got.DropoffH3R9) != wantH3PrefixLen {
		t.Errorf("h3 string lengths: pickup=%q dropoff=%q", got.PickupH3R9, got.DropoffH3R9)
	}
}
