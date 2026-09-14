// Command train-eta fits the ETA model on the Parquet traces the simulator
// writes. It reads every *.parquet under --traces, builds (distance, surge,
// hour) features against the actual trip_duration_s target, fits a ridge
// regression, and writes the model to --out.
//
//	go run ./cmd/train-eta --traces=traces --out=models/eta.json
//
// Then run the sim with --eta-model=models/eta.json to fill the predicted-ETA
// column and compare against actuals in the next batch of traces.
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/uber/h3-go/v4"

	"github.com/Deathslayer89/MetroSim/internal/eta"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/osm"
	"github.com/Deathslayer89/MetroSim/internal/pathfinding"
	"github.com/Deathslayer89/MetroSim/internal/tracelog"
)

func main() {
	tracesDir := flag.String("traces", "traces", "directory of *.parquet trace files")
	out := flag.String("out", "models/eta.json", "path to write the fitted model")
	lambda := flag.Float64("lambda", 1.0, "ridge regularization strength")
	sampleOSM := flag.String("sample-osm", "", "OSM .pbf path: generate training data by routing random reachable pairs instead of reading traces")
	samples := flag.Int("samples", 2000, "number of routed pairs to sample with --sample-osm")
	seed := flag.Int64("seed", 1, "RNG seed for --sample-osm")
	flag.Parse()

	var X [][]float64
	var y []float64

	if *sampleOSM != "" {
		X, y = sampleFromOSM(*sampleOSM, *samples, *seed)
	} else {
		X, y = loadFromTraces(*tracesDir)
	}

	model, err := eta.Fit(X, y, eta.FeatureNames, *lambda)
	if err != nil {
		log.Fatalf("fit: %v", err)
	}
	log.Printf("fit on %d rows: RMSE=%.1fs R2=%.3f", model.TrainN, model.RMSE, model.R2)
	for i, name := range model.Names {
		log.Printf("  weight[%s] = %+.3f (standardized)", name, model.Weights[i+1])
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	if err := model.Save(*out); err != nil {
		log.Fatalf("save: %v", err)
	}
	log.Printf("wrote model to %s", *out)
}

// loadFromTraces reads every *.parquet under dir and builds the training set
// from completed-trip rows.
func loadFromTraces(dir string) (X [][]float64, y []float64) {
	files, err := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if err != nil {
		log.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		log.Fatalf("no *.parquet under %s; run trace-writer against a Kafka run first, or use --sample-osm", dir)
	}
	var skipped int
	for _, path := range files {
		rows, err := readRows(path)
		if err != nil {
			log.Printf("skip %s: %v", filepath.Base(path), err)
			continue
		}
		for _, r := range rows {
			feat, target, ok := sample(r)
			if !ok {
				skipped++
				continue
			}
			X = append(X, feat)
			y = append(y, target)
		}
	}
	log.Printf("loaded %d usable trips (%d skipped) from %d trace file(s)", len(X), skipped, len(files))
	return X, y
}

// sampleFromOSM builds a training set by routing random node pairs. The target
// is the free-flow time of the route the simulator's planner picks; features
// are straight-line distance and a random hour, with surge fixed at 1.0.
func sampleFromOSM(pbfPath string, n int, seed int64) (X [][]float64, y []float64) {
	g, err := osm.LoadPBF(pbfPath)
	if err != nil {
		log.Fatalf("load osm: %v", err)
	}
	log.Printf("osm graph: %d nodes, %d edges; sampling %d routed pairs", g.NodeCount(), g.EdgeCount(), n)

	ids := make([]int, 0, g.NodeCount())
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	planner := pathfinding.NewPathPlanner(g, nil)
	rng := rand.New(rand.NewSource(seed))

	attempts := 0
	maxAttempts := n * 20
	for len(y) < n && attempts < maxAttempts {
		attempts++
		from := ids[rng.Intn(len(ids))]
		to := ids[rng.Intn(len(ids))]
		if from == to {
			continue
		}
		path, err := planner.FindPath(from, to)
		if err != nil || len(path) == 0 {
			continue
		}
		// No congestion feature, so the model under-predicts trips under load.
		var durationSec float64
		for _, e := range path {
			durationSec += e.BaseWeight
		}
		if durationSec <= 0 {
			continue
		}
		p, _ := g.GetNode(from)
		q, _ := g.GetNode(to)
		dist := graph.EuclideanDistance(p, q)
		hour := float64(rng.Intn(24))
		X = append(X, eta.Features(dist, 1.0, hour))
		y = append(y, durationSec)
	}
	log.Printf("routed %d pairs (%d attempts)", len(y), attempts)
	return X, y
}

// sample builds one row's features and target, or ok=false for an unusable row.
// Distance is between H3 r9 cell centers because traces store cells; serving
// uses exact node distance, a small mismatch --sample-osm avoids.
func sample(r tracelog.TripRow) (features []float64, target float64, ok bool) {
	if r.TripDurationSec <= 0 || r.PickupH3R9 == "" || r.DropoffH3R9 == "" {
		return nil, 0, false
	}
	dist := haversineCells(r.PickupH3R9, r.DropoffH3R9)
	if dist < 0 {
		return nil, 0, false
	}
	hour := float64(time.UnixMilli(r.MatchTsMs).UTC().Hour())
	return eta.Features(dist, r.SurgeAtMatch, hour), r.TripDurationSec, true
}

func readRows(path string) ([]tracelog.TripRow, error) {
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
	reader := parquet.NewGenericReader[tracelog.TripRow](pf)
	defer reader.Close()

	var out []tracelog.TripRow
	buf := make([]tracelog.TripRow, 512)
	for {
		n, err := reader.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

// haversineCells returns the great-circle distance in meters between the
// centers of two H3 cells, or -1 if either cell string is invalid.
func haversineCells(a, b string) float64 {
	ca, cb := h3.IndexFromString(a), h3.IndexFromString(b)
	la, err1 := h3.CellToLatLng(h3.Cell(ca))
	lb, err2 := h3.CellToLatLng(h3.Cell(cb))
	if err1 != nil || err2 != nil {
		return -1
	}
	return graph.HaversineMeters(la.Lat, la.Lng, lb.Lat, lb.Lng)
}
