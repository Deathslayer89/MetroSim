package tracelog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

// A crash before EndBatch leaves nothing on disk, so a restarted recorder must
// accept the redelivered trips instead of treating them as seen.
func TestRollingRecorderCrashMidBatchLosesUncommittedRecords(t *testing.T) {
	dir := t.TempDir()
	r1, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatalf("open r1: %v", err)
	}

	for i := int64(1); i <= 5; i++ {
		if err := r1.Record(testTrip(i)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	// Crash: drop r1 without EndBatch or Close, so nothing reaches disk.
	files, _ := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if len(files) != 0 {
		t.Fatalf("crash test: expected no files before EndBatch, found %d", len(files))
	}

	// Restart, replay (same IDs), confirm a fresh batch is written.
	r2, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatalf("open r2: %v", err)
	}
	if r2.seen.Len() != 0 {
		t.Fatalf("r2 should see 0 ids from disk (no committed file), got %d", r2.seen.Len())
	}
	for i := int64(1); i <= 5; i++ {
		if err := r2.Record(testTrip(i)); err != nil {
			t.Fatalf("re-record %d: %v", i, err)
		}
	}
	if err := r2.Close(); err != nil {
		t.Fatalf("close r2: %v", err)
	}

	if got := rowsOnDisk(t, dir); got != 5 {
		t.Errorf("after the redelivery: want 5 rows on disk, got %d", got)
	}
}

func testTrip(id int64) TripInput {
	base := time.Unix(1_700_000_000, 0)
	return TripInput{
		RideID:       id,
		Scenario:     "smoke",
		Policy:       "greedy",
		Seed:         7,
		RequestTs:    base,
		MatchTs:      base.Add(1 * time.Second),
		PickupTs:     base.Add(10 * time.Second),
		DropoffTs:    base.Add(100 * time.Second),
		PickupNode:   3,
		DropoffNode:  9,
		PickupLat:    37.7749,
		PickupLon:    -122.4194,
		DropoffLat:   37.7900,
		DropoffLon:   -122.4050,
		SurgeAtMatch: 1.2,
	}
}

func TestRollingRecorderIsIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	// Run 1 writes rides 1..5.
	r1, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatalf("open r1: %v", err)
	}
	for i := int64(1); i <= 5; i++ {
		if err := r1.Record(testTrip(i)); err != nil {
			t.Fatalf("r1 record %d: %v", i, err)
		}
	}
	if err := r1.Close(); err != nil {
		t.Fatalf("r1 close: %v", err)
	}

	// Run 2 gets 1..5 again, as Kafka redelivers after a restart, then 6..10.
	r2, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatalf("open r2: %v", err)
	}
	if got := r2.seen.Len(); got != 5 {
		t.Fatalf("r2 should have loaded 5 seen ids, got %d", got)
	}
	for i := int64(1); i <= 10; i++ {
		if err := r2.Record(testTrip(i)); err != nil {
			t.Fatalf("r2 record %d: %v", i, err)
		}
	}
	if err := r2.Close(); err != nil {
		t.Fatalf("r2 close: %v", err)
	}

	if got := rowsOnDisk(t, dir); got != 10 {
		t.Errorf("want 10 rows on disk, one per trip, got %d", got)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if len(files) != 2 {
		t.Errorf("want one file per run, got %d", len(files))
	}
}

// Ride IDs restart at 1 every run, so ride 1 from different runs is a different
// trip. A true redelivery is still dropped.
func TestRollingRecorderKeepsSameRideIDFromDifferentRuns(t *testing.T) {
	dir := t.TempDir()
	r1, err := NewRollingRecorder(dir)
	if err != nil {
		t.Fatalf("open r1: %v", err)
	}
	greedy, batch, later := testTrip(1), testTrip(1), testTrip(1)
	batch.Policy = "batch"
	later.RequestTs = later.RequestTs.Add(time.Hour)
	later.DropoffTs = later.DropoffTs.Add(time.Hour)
	for _, in := range []TripInput{greedy, batch, later, greedy} {
		if err := r1.Record(in); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if err := r1.Close(); err != nil {
		t.Fatalf("close r1: %v", err)
	}
	if got := rowsOnDisk(t, dir); got != 3 {
		t.Errorf("want 3 distinct trips on disk, got %d", got)
	}
}

func rowsOnDisk(t *testing.T, dir string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.parquet"))
	total := 0
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		pf, err := parquet.OpenFile(f, info.Size())
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		total += int(pf.NumRows())
		f.Close()
	}
	return total
}
