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

// tmpDir holds files being written, so only the final rename changes the trace directory's mtime.
const tmpDir = ".tmp"

// DefaultDedupCap is how many trip keys the dedup set keeps in memory before it evicts whole runs.
const DefaultDedupCap = 100_000

/**
 * tripKey identifies one trip record. Ride IDs restart every run, so the key
 * carries the run ID. Rows written before run IDs existed have none and fall
 * back to the run's labels and the trip's request and dropoff times.
 */
type tripKey struct {
	runID, scenario, policy string
	seed, rideID            int64
	requestMs, dropoffMs    int64
}

func (k tripKey) run() string {
	if k.runID != "" {
		return k.runID
	}
	return fmt.Sprintf("%s|%s|%d", k.scenario, k.policy, k.seed)
}

func keyOf(t TripInput) tripKey {
	return tripKey{t.RunID, t.Scenario, t.Policy, t.Seed, t.RideID, t.RequestTs.UnixMilli(), t.DropoffTs.UnixMilli()}
}

/**
 * RollingRecorder writes each Kafka fetch to its own Parquet file, synced in
 * dir/.tmp and renamed into dir before the fetch commits; startup deletes
 * leftover temp files. It skips trips already on disk, including ones other
 * replicas sharing the directory wrote.
 */
type RollingRecorder struct {
	dir         string
	instanceTag string // unique per process; keeps restarts from clobbering each other's files
	batchSeq    atomic.Uint64

	mu              sync.Mutex
	seen            *seenRuns
	scanned         map[string]bool // files, by name, whose keys are already in seen
	dirModTime      time.Time       // the directory's mtime when last listed
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
	if err := os.MkdirAll(filepath.Join(dir, tmpDir), 0o755); err != nil {
		return nil, err
	}
	unfinished, err := filepath.Glob(filepath.Join(dir, "*.parquet.tmp"))
	if err != nil {
		return nil, err
	}
	inTmp, err := filepath.Glob(filepath.Join(dir, tmpDir, "*.parquet"))
	if err != nil {
		return nil, err
	}
	unfinished = append(unfinished, inTmp...)
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
		seen:        newSeenRuns(capacity, readKeys),
		scanned:     make(map[string]bool),
	}
	if err := r.loadSeen(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", dir, err)
	}
	log.Printf("rolling recorder: %s loaded %d trip keys into dedup set (cap=%d)", dir, r.seen.len(), capacity)
	return r, nil
}

// loadSeen reads every file once, oldest first, to learn which runs each holds, so the newest runs stay resident.
func (r *RollingRecorder) loadSeen() error {
	files, err := filepath.Glob(filepath.Join(r.dir, "*.parquet"))
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		si, errI := os.Stat(files[i])
		sj, errJ := os.Stat(files[j])
		if errI != nil || errJ != nil {
			return files[i] < files[j]
		}
		return si.ModTime().Before(sj.ModTime())
	})
	for _, path := range files {
		if err := r.absorb(path); err != nil {
			return err
		}
	}
	return nil
}

// absorb adds the keys of one file to seen. An unreadable file is skipped, since a crash can leave a partial one.
func (r *RollingRecorder) absorb(path string) error {
	r.scanned[filepath.Base(path)] = true
	keys, err := readKeys(path)
	if err != nil {
		log.Printf("rolling recorder: skipping %s: %v", filepath.Base(path), err)
		return nil
	}
	for _, k := range keys {
		if err := r.seen.add(k, path); err != nil {
			return err
		}
	}
	return nil
}

func (r *RollingRecorder) dirMod() (time.Time, error) {
	info, err := os.Stat(r.dir)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// unchangedSince reports whether the directory's mtime is still m, and old enough that a coarse clock can't hide a later change.
func unchangedSince(now, m, known time.Time) bool {
	return !known.IsZero() && m.Equal(known) && now.Sub(m) > time.Second
}

/**
 * absorbOthers adds the keys from files other replicas wrote since the last
 * look. It skips listing the directory when its mtime shows no change since
 * then. It needs r.mu held.
 */
func (r *RollingRecorder) absorbOthers() error {
	mod, err := r.dirMod()
	if err != nil {
		return err
	}
	if unchangedSince(time.Now(), mod, r.dirModTime) {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(r.dir, "*.parquet"))
	if err != nil {
		return err
	}
	for _, path := range files {
		if r.scanned[filepath.Base(path)] {
			continue
		}
		if err := r.absorb(path); err != nil {
			return err
		}
	}
	r.dirModTime = mod
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
			keys = append(keys, tripKey{row.RunID, row.Scenario, row.Policy, row.Seed, row.RideID, row.RequestTsMs, row.DropoffTsMs})
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
	if seen, err := r.seen.has(k); err != nil || seen {
		return err
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
		seen, err := r.seen.has(keyOf(t))
		if err != nil {
			r.resetBatchState()
			return fmt.Errorf("dedup: %w", err)
		}
		if !seen {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) == 0 {
		r.resetBatchState()
		return nil
	}

	seq := r.batchSeq.Add(1)
	hour := time.Now().UTC().Format("2006-01-02T15")
	name := fmt.Sprintf("trips_%s_%s_%06d.parquet", hour, r.instanceTag, seq)
	path := filepath.Join(r.dir, name)
	tmp := filepath.Join(r.dir, tmpDir, name)

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
	before, statErr := r.dirMod()
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
		if err := r.seen.add(keyOf(t), path); err != nil {
			log.Printf("rolling recorder: %v", err)
		}
	}
	// If only our rename changed the directory since the last look, the next batch needn't list it.
	if after, err := r.dirMod(); statErr == nil && err == nil && before.Equal(r.dirModTime) {
		r.dirModTime = after
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
				input.RunID = c.Meta.RunId
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
