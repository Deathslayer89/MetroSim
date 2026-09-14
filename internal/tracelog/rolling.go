package tracelog

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/Deathslayer89/MetroSim/internal/events"
	eventspb "github.com/Deathslayer89/MetroSim/proto/events"
)

// DefaultDedupCap bounds the in-memory dedup set. Keys are evicted oldest
// first, so a trip redelivered after its key was evicted is written twice.
const DefaultDedupCap = 100_000

// tripKey identifies one trip record. Ride IDs restart every run, so the key
// also carries the run's labels and the trip's request and dropoff times.
type tripKey struct {
	scenario, policy     string
	seed, rideID         int64
	requestMs, dropoffMs int64
}

func keyOf(t TripInput) tripKey {
	return tripKey{t.Scenario, t.Policy, t.Seed, t.RideID, t.RequestTs.UnixMilli(), t.DropoffTs.UnixMilli()}
}

// RollingRecorder writes each Kafka fetch to its own Parquet file, synced and
// renamed from a .tmp name before the fetch commits; startup deletes leftover
// .tmp files. It skips trips already on disk, including ones other replicas
// sharing the directory wrote.
type RollingRecorder struct {
	dir         string
	instanceTag string // unique per process; keeps restarts from clobbering each other's files
	batchSeq    atomic.Uint64

	mu              sync.Mutex
	seen            *boundedSet[tripKey]
	scanned         map[string]bool // files, by name, whose keys are already in seen
	currentBuf      []TripInput
	currentBatchIDs map[tripKey]struct{} // keys in currentBuf, so a redelivered record isn't buffered twice
}

func NewRollingRecorder(dir string) (*RollingRecorder, error) {
	return NewRollingRecorderWithCap(dir, DefaultDedupCap)
}

func NewRollingRecorderWithCap(dir string, capacity int) (*RollingRecorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	unfinished, err := filepath.Glob(filepath.Join(dir, "*.parquet.tmp"))
	if err != nil {
		return nil, err
	}
	for _, p := range unfinished {
		if err := os.Remove(p); err != nil {
			return nil, err
		}
	}
	if len(unfinished) > 0 {
		log.Printf("rolling recorder: removed %d unfinished files from %s", len(unfinished), dir)
	}
	r := &RollingRecorder{
		dir:         dir,
		instanceTag: fmt.Sprintf("%d", time.Now().UnixNano()),
		seen:        newBoundedSet[tripKey](capacity),
		scanned:     make(map[string]bool),
	}
	if err := r.loadSeen(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", dir, err)
	}
	log.Printf("rolling recorder: %s loaded %d trip keys into dedup set (cap=%d)", dir, r.seen.Len(), capacity)
	return r, nil
}

func (r *RollingRecorder) loadSeen() error {
	files, err := filepath.Glob(filepath.Join(r.dir, "*.parquet"))
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		si, errI := os.Stat(files[i])
		sj, errJ := os.Stat(files[j])
		if errI != nil || errJ != nil {
			return files[i] > files[j]
		}
		return si.ModTime().After(sj.ModTime())
	})
	// Read the newest files until the set is full, then add them oldest first:
	// the set evicts in insertion order, so the newest keys last longest.
	var perFile [][]tripKey
	total := 0
	for _, path := range files {
		r.scanned[filepath.Base(path)] = true
		if total >= r.seen.capacity {
			continue
		}
		keys, err := readKeys(path)
		if err != nil {
			log.Printf("rolling recorder: skipping %s: %v", filepath.Base(path), err)
			continue
		}
		perFile = append(perFile, keys)
		total += len(keys)
	}
	for i := len(perFile) - 1; i >= 0; i-- {
		for _, k := range perFile[i] {
			r.seen.Add(k)
		}
	}
	return nil
}

// absorbOthers adds the keys from files that appeared since the last look,
// which are other replicas'. It needs r.mu held.
func (r *RollingRecorder) absorbOthers() error {
	files, err := filepath.Glob(filepath.Join(r.dir, "*.parquet"))
	if err != nil {
		return err
	}
	for _, path := range files {
		name := filepath.Base(path)
		if r.scanned[name] {
			continue
		}
		r.scanned[name] = true
		keys, err := readKeys(path)
		if err != nil {
			log.Printf("rolling recorder: skipping %s: %v", name, err)
			continue
		}
		for _, k := range keys {
			r.seen.Add(k)
		}
	}
	return nil
}

func readKeys(path string) ([]tripKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, errors.New("empty file")
	}
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return nil, err
	}
	reader := parquet.NewGenericReader[TripRow](pf)
	defer reader.Close()
	var keys []tripKey
	buf := make([]TripRow, 256)
	for {
		n, err := reader.Read(buf)
		for _, row := range buf[:n] {
			keys = append(keys, tripKey{row.Scenario, row.Policy, row.Seed, row.RideID, row.RequestTsMs, row.DropoffTsMs})
		}
		if errors.Is(err, io.EOF) {
			return keys, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// Record buffers a trip for the current batch; EndBatch writes it. A trip that
// is already on disk or already buffered is ignored, so retries can't grow the
// buffer.
func (r *RollingRecorder) Record(input TripInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := keyOf(input)
	if r.seen.Has(k) {
		return nil
	}
	if r.currentBatchIDs == nil {
		r.currentBatchIDs = make(map[tripKey]struct{})
	}
	if _, dup := r.currentBatchIDs[k]; dup {
		return nil
	}
	r.currentBuf = append(r.currentBuf, input)
	r.currentBatchIDs[k] = struct{}{}
	return nil
}

// resetBatchState runs on every EndBatch exit, so a failed batch doesn't leak
// into the next attempt.
func (r *RollingRecorder) resetBatchState() {
	r.currentBuf = r.currentBuf[:0]
	r.currentBatchIDs = nil
}

// EndBatch writes the batch to its own Parquet file and marks its trips seen.
// On error nothing is marked, and the caller must not commit offsets.
func (r *RollingRecorder) EndBatch() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.currentBuf) == 0 {
		return nil
	}
	if err := r.absorbOthers(); err != nil {
		r.resetBatchState()
		return fmt.Errorf("scan %s: %w", r.dir, err)
	}
	fresh := make([]TripInput, 0, len(r.currentBuf))
	for _, t := range r.currentBuf {
		if !r.seen.Has(keyOf(t)) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) == 0 {
		r.resetBatchState()
		return nil
	}

	seq := r.batchSeq.Add(1)
	hour := time.Now().UTC().Format("2006-01-02T15")
	path := filepath.Join(r.dir, fmt.Sprintf("trips_%s_%s_%06d.parquet", hour, r.instanceTag, seq))
	tmp := path + ".tmp"

	rec, err := Open(tmp, "", "", 0)
	if err != nil {
		r.resetBatchState() // partial file never created; drop the buffer so retry can rebuild
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	for _, t := range fresh {
		if err := rec.Record(t); err != nil {
			rec.Close()
			os.Remove(tmp)
			r.resetBatchState()
			return fmt.Errorf("write batch: %w", err)
		}
	}
	if err := rec.Close(); err != nil {
		os.Remove(tmp)
		r.resetBatchState()
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		r.resetBatchState()
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	// Syncing the directory makes the rename itself survive a power cut.
	if err := syncDir(r.dir); err != nil {
		log.Printf("rolling recorder: sync %s: %v", r.dir, err)
	}

	// Mark trips seen only once the footer is written.
	r.scanned[filepath.Base(path)] = true
	for _, t := range fresh {
		r.seen.Add(keyOf(t))
	}
	r.resetBatchState()
	return nil
}

// Close flushes any pending buffered records as a final batch.
func (r *RollingRecorder) Close() error {
	return r.EndBatch()
}

// SubscribeToBus wires the recorder under the "trace-writer" group with batch
// boundaries. The Kafka bus only commits offsets after EndBatch succeeds.
func (r *RollingRecorder) SubscribeToBus(bus events.Bus) error {
	return events.SubscribeTripCompletedBatched(bus, "trace-writer",
		func(c *eventspb.TripCompleted) {
			input := TripInput{
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
			}
			if c.Meta != nil {
				input.Scenario = c.Meta.Scenario
				input.Policy = c.Meta.Policy
				input.Seed = c.Meta.Seed
			}
			if err := r.Record(input); err != nil {
				log.Printf("trace-writer: record ride %d: %v", c.RideId, err)
			}
		},
		func() error {
			if err := r.EndBatch(); err != nil {
				log.Printf("trace-writer: end batch: %v", err)
				return err
			}
			return nil
		},
	)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
