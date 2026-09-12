// Command experiment runs dispatch policies over the same scenario and seeds,
// then reports per-seed mean pickup waits side by side. Given several --demand
// multipliers, it does that at each one and reports how the comparison moves
// as demand grows.
package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/eta"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/osm"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
	"github.com/Deathslayer89/MetroSim/internal/simulation"
	"github.com/Deathslayer89/MetroSim/internal/stats"
	"github.com/Deathslayer89/MetroSim/internal/tracelog"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
)

type runResult struct {
	seed      int64
	waits     []float64 // one per rider; the first pickedUp are riders who were picked up
	pickedUp  int
	requested int
	completed int
	moves     int // repositioning moves
}

type policySummary struct {
	name     string
	runs     []runResult
	allWaits []float64
}

// level holds the runs at one demand multiplier: the scenario with its arrival
// rates scaled, and a summary per policy.
type level struct {
	demand float64
	sc     *scenario.Scenario
	pols   []*policySummary
}

type provenance struct {
	graph, scenario, command, commit string
}

func main() {
	scenarioPath := flag.String("scenario", "scenarios/baseline.yaml", "scenario YAML")
	osmPath := flag.String("osm", "", "OSM .pbf path; the CSV test grid if empty")
	policiesArg := flag.String("policies", "greedy,batch", "comma-separated policies (greedy, batch, region-sharded, each optionally +reposition); the first is the baseline")
	demandArg := flag.String("demand", "1", "comma-separated multipliers on the scenario's arrival rates; each gets every policy and seed")
	replicates := flag.Int("replicates", 30, "seeds per policy")
	parallel := flag.Int("parallel", runtime.NumCPU(), "runs to execute at once")
	batchWindow := flag.Duration("batch-window", 3*time.Second, "batch window")
	repositionAfter := flag.Duration("reposition-after", 2*time.Minute, "idle time before a car repositions, for +reposition policies")
	outDir := flag.String("out", "experiments", "output directory root")
	traceDir := flag.String("trace-dir", "", "if set, write per-run Parquet traces under this directory")
	driverAcceptRate := flag.Float64("driver-accept-rate", 0, "P(driver accepts), 0 disables (always accept)")
	driverCancelRate := flag.Float64("driver-cancel-rate", 0, "per-tick P(driver cancels before pickup), 0 disables")
	etaModelPath := flag.String("eta-model", "", "trained ETA model (cmd/train-eta); fills eta_predicted_s in traces")
	flag.Parse()

	demands, err := parseDemands(*demandArg)
	if err != nil {
		log.Fatalf("--demand: %v", err)
	}
	prov := provenance{
		scenario: *scenarioPath,
		command:  strings.TrimSpace("go run ./cmd/experiment " + strings.Join(os.Args[1:], " ")),
		commit:   gitCommit(),
	}
	behavior := dispatcher.DriverBehavior{AcceptRate: *driverAcceptRate, CancelRate: *driverCancelRate}

	var etaModel *eta.Model
	if *etaModelPath != "" {
		m, err := eta.Load(*etaModelPath)
		if err != nil {
			log.Fatalf("eta model: %v", err)
		}
		etaModel = m
		log.Printf("eta model: loaded %s (train R2=%.3f RMSE=%.0fs)", *etaModelPath, m.R2, m.RMSE)
	}

	g, desc, err := loadGraph(*osmPath)
	if err != nil {
		log.Fatalf("graph: %v", err)
	}
	prov.graph = desc
	log.Printf("graph: %d nodes, %d edges", g.NodeCount(), g.EdgeCount())

	sc, err := scenario.Load(*scenarioPath)
	if err != nil {
		log.Fatalf("scenario: %v", err)
	}
	log.Printf("scenario: %s (duration=%s, vehicles=%d)", sc.Name, sc.Duration, sc.Vehicles.Count)

	runDir := filepath.Join(*outDir, fmt.Sprintf("%s_%s", sc.Name, time.Now().UTC().Format("20060102_150405")))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", runDir, err)
	}
	log.Printf("output: %s", runDir)

	var levels []*level
	for _, k := range demands {
		lv := &level{demand: k, sc: scaled(sc, k)}
		for _, name := range strings.Split(*policiesArg, ",") {
			lv.pols = append(lv.pols, &policySummary{name: strings.TrimSpace(name), runs: make([]runResult, *replicates)})
		}
		levels = append(levels, lv)
	}

	type job struct {
		lv  *level
		s   *policySummary
		rep int
	}
	jobs := make(chan job)
	errs := make(chan error, len(levels)*len(levels[0].pols)*(*replicates))
	var wg sync.WaitGroup
	for w := 0; w < max(1, *parallel); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				seed := sc.Seed + int64(j.rep)
				res, err := runHeadless(g, j.lv.sc, j.s.name, *batchWindow, *repositionAfter, seed, *traceDir, behavior, etaModel)
				if err != nil {
					errs <- fmt.Errorf("%s policy=%s seed=%d: %w", j.lv.sc.Name, j.s.name, seed, err)
					continue
				}
				j.s.runs[j.rep] = res
				log.Printf("[%s %s seed %d] requested=%d picked_up=%d completed=%d mean_wait=%.1fs",
					j.lv.sc.Name, j.s.name, seed, res.requested, res.pickedUp, res.completed, stats.Mean(res.waits))
			}
		}()
	}
	// The busiest level's runs take longest, so they start first.
	for i := len(levels) - 1; i >= 0; i-- {
		for _, s := range levels[i].pols {
			for r := 0; r < *replicates; r++ {
				jobs <- job{levels[i], s, r}
			}
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		log.Fatal(err)
	}
	for _, lv := range levels {
		for _, s := range lv.pols {
			for _, r := range s.runs {
				s.allWaits = append(s.allWaits, r.waits...)
			}
		}
	}

	if err := writeWaitsCSV(filepath.Join(runDir, "waits.csv"), levels); err != nil {
		log.Fatalf("csv: %v", err)
	}
	for _, lv := range levels {
		byName := make(map[string]*policySummary, len(lv.pols))
		for _, s := range lv.pols {
			byName[s.name] = s
		}
		if err := writeCDFSVG(filepath.Join(runDir, cdfFile(lv, len(levels))), byName); err != nil {
			log.Fatalf("svg: %v", err)
		}
	}
	if len(levels) > 1 {
		if err := writeDemandSVG(filepath.Join(runDir, "demand.svg"), levels); err != nil {
			log.Fatalf("svg: %v", err)
		}
	}
	if err := writeReport(filepath.Join(runDir, "report.md"), sc, prov, levels, *batchWindow); err != nil {
		log.Fatalf("report: %v", err)
	}
	log.Printf("wrote %s", filepath.Join(runDir, "report.md"))
}

// parseDemands reads --demand: positive multipliers, none repeated, returned in
// increasing order.
func parseDemands(arg string) ([]float64, error) {
	var out []float64
	for _, field := range strings.Split(arg, ",") {
		k, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil || !(k > 0) || math.IsInf(k, 1) {
			return nil, fmt.Errorf("%q is not a positive number", field)
		}
		for _, prev := range out {
			if prev == k {
				return nil, fmt.Errorf("%g is listed twice", k)
			}
		}
		out = append(out, k)
	}
	sort.Float64s(out)
	return out, nil
}

// scaled returns a copy of sc with every arrival rate multiplied by k, named
// after k unless it's 1 so its traces and events don't pass for the original's.
func scaled(sc *scenario.Scenario, k float64) *scenario.Scenario {
	c := *sc
	if k != 1 {
		c.Name = fmt.Sprintf("%s_%gx", sc.Name, k)
	}
	c.Arrivals.RateSegments = make([]scenario.RatePoint, len(sc.Arrivals.RateSegments))
	for i, p := range sc.Arrivals.RateSegments {
		c.Arrivals.RateSegments[i] = scenario.RatePoint{T: p.T, Rate: k * p.Rate}
	}
	return &c
}

// cdfFile names a level's wait CDF; with one level there's nothing to tell apart.
func cdfFile(lv *level, levels int) string {
	if levels == 1 {
		return "wait_cdf.svg"
	}
	return fmt.Sprintf("wait_cdf_%gx.svg", lv.demand)
}

func loadGraph(osmPath string) (*graph.Graph, string, error) {
	if osmPath != "" {
		g, err := osm.LoadPBF(osmPath)
		if err != nil {
			return nil, "", err
		}
		return g, fmt.Sprintf("`%s`, %d nodes, %d edges", osmPath, g.NodeCount(), g.EdgeCount()), nil
	}
	g, err := graph.LoadGraphFromCSV("data/graphs/test_nodes.csv", "data/graphs/test_edges.csv")
	if err != nil {
		return nil, "", err
	}
	return g, fmt.Sprintf("CSV test grid, %d nodes, %d edges", g.NodeCount(), g.EdgeCount()), nil
}

// gitCommit names the checked-out commit, marked dirty when tracked files have
// uncommitted changes.
func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	rev := strings.TrimSpace(string(out))
	st, err := exec.Command("git", "status", "--porcelain", "--untracked-files=no").Output()
	if err == nil && len(bytes.TrimSpace(st)) > 0 {
		rev += "-dirty"
	}
	return rev
}

// runHeadless runs one policy on one seed. After the scenario ends it keeps
// ticking for up to half its duration so trips in progress can finish. A rider
// still aboard then counts with the wait they had. A rider still waiting counts
// with the time they had waited so far, which understates their wait but keeps
// a policy that strands riders from looking faster than one that serves them.
func runHeadless(g *graph.Graph, sc *scenario.Scenario, polName string, batchWindow, repositionAfter time.Duration, seed int64, traceDir string, behavior dispatcher.DriverBehavior, etaModel *eta.Model) (runResult, error) {
	const tickRate = 10.0
	const tickDt = time.Second / tickRate

	engine := simulation.NewEngine(simulation.Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         tickRate,
		SpeedMultiplier:  1.0,
	})
	base, reposition := strings.CutSuffix(polName, "+reposition")
	switch base {
	case "greedy":
	case "batch":
		engine.GetDispatcher().SetPolicy(dispatcher.NewBatchPolicy(batchWindow))
	case "region-sharded":
		engine.GetDispatcher().SetPolicy(dispatcher.NewRegionShardedBatchPolicy(batchWindow))
	default:
		return runResult{}, fmt.Errorf("unknown policy %q", polName)
	}
	if reposition {
		engine.GetDispatcher().SetRepositioning(dispatcher.DefaultRepositioning(repositionAfter))
	}
	engine.SetRunInfo(events.RunInfo{Scenario: sc.Name, Policy: polName, Seed: seed})
	if behavior.AcceptRate > 0 || behavior.CancelRate > 0 {
		engine.GetDispatcher().SetDriverBehavior(behavior, seed)
	}
	if etaModel != nil {
		engine.GetDispatcher().SetETAModel(etaModel)
	}

	if traceDir != "" {
		_ = os.MkdirAll(traceDir, 0o755)
		path := filepath.Join(traceDir, fmt.Sprintf("run_%s_%s_seed%d.parquet", sc.Name, polName, seed))
		rec, err := tracelog.Open(path, sc.Name, polName, seed)
		if err != nil {
			return runResult{}, fmt.Errorf("trace open: %w", err)
		}
		defer rec.Close()
		rec.SubscribeToBus(engine.Bus())
	}

	scCopy := *sc
	scCopy.Seed = seed
	if scCopy.StartTime.IsZero() {
		scCopy.StartTime = time.Unix(0, 0)
	}
	engine.SetStartTime(scCopy.StartTime)

	gen, err := scenario.NewGenerator(&scCopy, g)
	if err != nil {
		return runResult{}, err
	}
	engine.SetArrivalSource(gen)
	for _, node := range gen.VehicleSpawnNodes(g) {
		engine.SpawnVehicle(node)
	}

	drainCap := sc.Duration + sc.Duration/2
	ticks := int(drainCap / tickDt)
	d := engine.GetDispatcher()
	for tick := 0; tick < ticks; tick++ {
		engine.Tick()
		elapsed := time.Duration(tick+1) * tickDt
		if elapsed > sc.Duration && d.GetActiveRideCount() == 0 && d.GetPendingCount() == 0 {
			break
		}
	}

	completed := d.GetCompletedRides()
	aboard := d.RidesInProgress()
	waiting := d.Waiting()
	end := engine.GetCurrentTime()
	if reposition {
		log.Printf("  [%s seed=%d] %d repositioning moves", polName, seed, d.RepositionCount())
	}
	if behavior.AcceptRate > 0 || behavior.CancelRate > 0 {
		bs := d.BehaviorStats()
		log.Printf("  [%s seed=%d] driver friction: %d declines, %d cancellations", polName, seed, bs.Declines, bs.Cancellations)
	}
	waits := make([]float64, 0, len(completed)+len(aboard)+len(waiting))
	for _, r := range append(completed, aboard...) {
		waits = append(waits, r.PickupTime.Sub(r.Request.RequestTime).Seconds())
	}
	pickedUp := len(waits)
	for _, req := range waiting {
		waits = append(waits, end.Sub(req.RequestTime).Seconds())
	}
	return runResult{seed: seed, waits: waits, pickedUp: pickedUp, requested: gen.RequestCount(), completed: len(completed), moves: d.RepositionCount()}, nil
}

func writeWaitsCSV(path string, levels []*level) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"demand", "policy", "seed", "wait_seconds", "picked_up"}); err != nil {
		return err
	}
	for _, lv := range levels {
		demand := strconv.FormatFloat(lv.demand, 'g', -1, 64)
		for _, s := range lv.pols {
			for _, r := range s.runs {
				for k, x := range r.waits {
					row := []string{demand, s.name, strconv.FormatInt(r.seed, 10), strconv.FormatFloat(x, 'f', 4, 64), strconv.FormatBool(k < r.pickedUp)}
					if err := w.Write(row); err != nil {
						return err
					}
				}
			}
		}
	}
	w.Flush()
	return w.Error()
}

func writeReport(path string, sc *scenario.Scenario, prov provenance, levels []*level, window time.Duration) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	pols := levels[0].pols
	names := make([]string, len(pols))
	for i, s := range pols {
		names[i] = s.name
	}
	seeds := len(pols[0].runs)
	multipliers := make([]string, len(levels))
	for i, lv := range levels {
		multipliers[i] = fmt.Sprintf("%gx", lv.demand)
	}

	title, demand := strings.Join(names, " vs "), ""
	switch {
	case len(levels) > 1:
		title += " as demand grows"
		demand = ", arrival rates times " + strings.Join(multipliers, ", ")
	case levels[0].demand != 1:
		demand = ", arrival rates times " + multipliers[0]
	}
	fmt.Fprintf(f, "# %s: %s\n\n", sc.Name, title)
	fmt.Fprintf(f, "| | |\n|---|---|\n")
	fmt.Fprintf(f, "| graph | %s |\n", prov.graph)
	fmt.Fprintf(f, "| scenario | `%s`, %g min, %d vehicles%s |\n", prov.scenario, sc.Duration.Minutes(), sc.Vehicles.Count, demand)
	fmt.Fprintf(f, "| seeds | %d to %d, each run once per policy%s |\n", sc.Seed, sc.Seed+int64(seeds)-1, map[bool]string{true: " and demand"}[len(levels) > 1])
	fmt.Fprintf(f, "| command | `%s` |\n", prov.command)
	fmt.Fprintf(f, "| commit | `%s` |\n", prov.commit)

	if len(levels) == 1 {
		writeLevel(f, levels[0], "##", cdfFile(levels[0], 1))
		return nil
	}
	writeSummary(f, levels, window)
	for _, lv := range levels {
		fmt.Fprintf(f, "\n## %gx demand\n", lv.demand)
		writeLevel(f, lv, "###", cdfFile(lv, len(levels)))
	}
	return nil
}

// writeSummary gives each demand level a row: requests per run, how many arrive
// in one batch window, each policy's mean wait, and each other policy's paired
// difference from the first.
func writeSummary(f *os.File, levels []*level, window time.Duration) {
	pols := levels[0].pols
	fmt.Fprintf(f, "\n## Mean wait by demand\n\n![mean wait by demand](demand.svg)\n\n")
	head := fmt.Sprintf("| demand | requests per run | arrivals per %g s window |", window.Seconds())
	rule := "|---:|---:|---:|"
	for _, s := range pols {
		head += fmt.Sprintf(" %s mean wait (s) |", s.name)
		rule += "---:|"
	}
	for _, alt := range pols[1:] {
		head += fmt.Sprintf(" %s minus %s (s) | 95%% CI | %s lower in |", alt.name, pols[0].name, alt.name)
		rule += "---:|---:|---:|"
	}
	fmt.Fprintln(f, head)
	fmt.Fprintln(f, rule)
	for _, lv := range levels {
		var req int
		for _, r := range lv.pols[0].runs {
			req += r.requested
		}
		perRun := float64(req) / float64(len(lv.pols[0].runs))
		row := fmt.Sprintf("| %gx | %.0f | %.2f |", lv.demand, perRun, perRun/lv.sc.Duration.Seconds()*window.Seconds())
		for _, s := range lv.pols {
			row += fmt.Sprintf(" %.1f |", stats.Mean(s.allWaits))
		}
		for _, alt := range lv.pols[1:] {
			diffs, wins, ties := seedDiffs(lv.pols[0], alt)
			lo, hi := bootstrapCI(diffs)
			row += fmt.Sprintf(" %+.1f | [%+.1f, %+.1f] | %d of %d |", stats.Mean(diffs), lo, hi, wins, len(diffs)-ties)
		}
		fmt.Fprintln(f, row)
	}
	fmt.Fprintln(f, "\nDifferences are per seed, paired, with a 95% bootstrap interval across seeds. The sections below have each level in full.")
}

// writeLevel reports one demand level: every trip per policy, then each other
// policy against the first, seed by seed. Its headings start at h.
func writeLevel(f *os.File, lv *level, h, cdf string) {
	fmt.Fprintf(f, "\n%s All trips\n\n", h)
	fmt.Fprintln(f, "| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |")
	fmt.Fprintln(f, "|---|---:|---:|---:|---:|---:|---:|")
	for _, s := range lv.pols {
		var req, up, done int
		for _, r := range s.runs {
			req += r.requested
			up += r.pickedUp
			done += r.completed
		}
		fmt.Fprintf(f, "| %s | %d | %d (%.1f%%) | %d | %.1f | %.1f | %.1f |\n", s.name, req, up, percent(up, req), done,
			stats.Mean(s.allWaits), stats.Percentile(s.allWaits, 0.5), stats.Percentile(s.allWaits, 0.95))
	}
	fmt.Fprintln(f, "\nWaits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.")
	for _, s := range lv.pols {
		var moves int
		for _, r := range s.runs {
			moves += r.moves
		}
		if moves > 0 {
			fmt.Fprintf(f, "\n%s made %d repositioning moves, %.0f per run.\n", s.name, moves, float64(moves)/float64(len(s.runs)))
		}
	}

	for _, alt := range lv.pols[1:] {
		writePaired(f, h, lv.pols[0], alt)
	}

	fmt.Fprintf(f, "\n%s Wait-time CDF, all trips\n\n![wait_cdf](%s)\n", h, cdf)
}

// writePaired compares alt with base seed by seed. A seed where either policy
// had no riders has no mean wait, so it is left out.
func writePaired(f *os.File, h string, base, alt *policySummary) {
	fmt.Fprintf(f, "\n%s %s vs %s: mean wait per seed (s)\n\n| seed | %s | %s | difference |\n|---:|---:|---:|---:|\n",
		h, alt.name, base.name, base.name, alt.name)
	for i := range base.runs {
		a, b := base.runs[i].waits, alt.runs[i].waits
		if len(a) == 0 || len(b) == 0 {
			fmt.Fprintf(f, "| %d | | | no riders |\n", base.runs[i].seed)
			continue
		}
		fmt.Fprintf(f, "| %d | %.1f | %.1f | %+.1f |\n", base.runs[i].seed, stats.Mean(a), stats.Mean(b), stats.Mean(b)-stats.Mean(a))
	}
	diffs, wins, ties := seedDiffs(base, alt)
	if len(diffs) == 0 {
		fmt.Fprintln(f, "\nNo seed had riders under both policies.")
		return
	}
	lo, hi := bootstrapCI(diffs)
	fmt.Fprintf(f, "\nMean difference, %s minus %s: %+.1f s, 95%% bootstrap CI [%+.1f, %+.1f] across seeds.\n\n",
		alt.name, base.name, stats.Mean(diffs), lo, hi)
	fmt.Fprintf(f, "%s had the lower mean wait in %d of %d seeds; two-sided sign test p = %.3g.\n",
		alt.name, wins, len(diffs)-ties, stats.SignTest(wins, len(diffs)-ties))
	if skipped := len(base.runs) - len(diffs); skipped > 0 {
		fmt.Fprintf(f, "\n%d seeds left out because a policy had no riders.\n", skipped)
	}
}

// seedDiffs returns alt's mean wait minus base's for each seed where both had
// riders, and in how many of those alt was lower or tied.
func seedDiffs(base, alt *policySummary) (diffs []float64, wins, ties int) {
	for i := range base.runs {
		a, b := base.runs[i].waits, alt.runs[i].waits
		if len(a) == 0 || len(b) == 0 {
			continue
		}
		d := stats.Mean(b) - stats.Mean(a)
		diffs = append(diffs, d)
		switch {
		case d < 0:
			wins++
		case d == 0:
			ties++
		}
	}
	return diffs, wins, ties
}

func bootstrapCI(diffs []float64) (lo, hi float64) {
	return stats.BootstrapMeanCI(diffs, 10000, rand.New(rand.NewSource(1)))
}

func percent(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
