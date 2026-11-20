package tracelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func recordAll(t *testing.T, r *RollingRecorder, trips ...TripInput) {
	t.Helper()
	for _, in := range trips {
		if err := r.Record(in); err != nil {
			t.Fatalf("record ride %d: %v", in.RideID, err)
		}
	}
	if err := r.EndBatch(); err != nil {
		t.Fatalf("end batch: %v", err)
	}
}

func tripInRun(id int64, run string) TripInput {
	trip := testTrip(id)
	trip.RunID = run
	return trip
}

// Two runs of the same seed produce identical trips; only the run ID tells them apart.
func TestRollingRecorderWritesARerunOfTheSameSeed(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	recordAll(t, r, tripInRun(1, "run-a"), tripInRun(2, "run-a"))
	recordAll(t, r, tripInRun(1, "run-b"), tripInRun(2, "run-b"))
	recordAll(t, r, tripInRun(1, "run-b"))
	if got := rowsOnDisk(t, dir); got != 4 {
		t.Errorf("want 4 trips from two runs, got %d", got)
	}
}

func TestRollingRecorderReloadsAnEvictedRunInsteadOfDuplicating(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRollingRecorderWithCap(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	recordAll(t, r, tripInRun(1, "old"), tripInRun(2, "old"), tripInRun(3, "old"))
	recordAll(t, r, tripInRun(1, "new"), tripInRun(2, "new"), tripInRun(3, "new"))
	if _, resident := r.seen.resident["old"]; resident {
		t.Fatal("run old should have been evicted at cap 3")
	}
	recordAll(t, r, tripInRun(2, "old"), tripInRun(4, "old"))
	if got := rowsOnDisk(t, dir); got != 7 {
		t.Errorf("want 7 trips on disk, got %d", got)
	}
}

func TestRollingRecorderKeepsDedupAcrossRestartPastTheCap(t *testing.T) {
	dir := t.TempDir()
	r1, err := NewRollingRecorderWithCap(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	recordAll(t, r1, tripInRun(1, "a"), tripInRun(2, "a"))
	recordAll(t, r1, tripInRun(1, "b"), tripInRun(2, "b"))
	r2, err := NewRollingRecorderWithCap(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	recordAll(t, r2, tripInRun(1, "a"), tripInRun(2, "b"))
	if got := rowsOnDisk(t, dir); got != 4 {
		t.Errorf("want 4 trips after a restart and redelivery, got %d", got)
	}
}

// legacyRow is TripRow as written before the run_id column existed.
type legacyRow struct {
	RideID      int64  `parquet:"ride_id"`
	Scenario    string `parquet:"scenario"`
	Policy      string `parquet:"policy"`
	Seed        int64  `parquet:"seed"`
	RequestTsMs int64  `parquet:"request_ts_unix_ms"`
	DropoffTsMs int64  `parquet:"dropoff_ts_unix_ms"`
}

func TestRollingRecorderReadsFilesWithoutRunID(t *testing.T) {
	dir := t.TempDir()
	trip := testTrip(1)
	row := legacyRow{trip.RideID, trip.Scenario, trip.Policy, trip.Seed, trip.RequestTs.UnixMilli(), trip.DropoffTs.UnixMilli()}
	if err := parquet.WriteFile(filepath.Join(dir, "trips_old.parquet"), []legacyRow{row}); err != nil {
		t.Fatal(err)
	}
	r, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	recordAll(t, r, trip)
	if got := rowsOnDisk(t, dir); got != 1 {
		t.Errorf("a pre-run-ID trip was written again: %d rows", got)
	}
}

func TestUnchangedSinceNeedsAMatchingMtimeOlderThanASecond(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Second)
	cases := []struct {
		name       string
		mod, known time.Time
		want       bool
	}{
		{"never listed", old, time.Time{}, false},
		{"changed", old, old.Add(-time.Second), false},
		{"same and old", old, old, true},
		{"same but recent", now, now, false},
	}
	for _, c := range cases {
		if got := unchangedSince(now, c.mod, c.known); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRollingRecorderSkipsListingAnUnchangedDirectory(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	recordAll(t, r, tripInRun(1, "a"))
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.dirModTime = past.Truncate(0)
	if mod, _ := r.dirMod(); !mod.Equal(r.dirModTime) {
		r.dirModTime = mod
	}
	r.scanned = map[string]bool{}
	err = r.absorbOthers()
	listed := len(r.scanned)
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if listed != 0 {
		t.Errorf("listed %d files in a directory that hadn't changed", listed)
	}
}
