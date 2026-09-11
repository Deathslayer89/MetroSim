package main

import (
	"context"
	"flag"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Deathslayer89/MetroSim/internal/dispatcher"
	"github.com/Deathslayer89/MetroSim/internal/eta"
	"github.com/Deathslayer89/MetroSim/internal/events"
	"github.com/Deathslayer89/MetroSim/internal/events/kafka"
	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/osm"
	"github.com/Deathslayer89/MetroSim/internal/promexport"
	"github.com/Deathslayer89/MetroSim/internal/scenario"
	"github.com/Deathslayer89/MetroSim/internal/server"
	"github.com/Deathslayer89/MetroSim/internal/simulation"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
)

func main() {
	osmPath := flag.String("osm", "", "path to a .osm.pbf extract; falls back to the CSV grid if empty")
	scenarioPath := flag.String("scenario", "", "path to a scenario YAML; falls back to a hardcoded demo if empty")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	metricsAddr := flag.String("metrics-addr", ":9100", "Prometheus listen address with --bus=kafka")
	policy := flag.String("policy", "greedy", "dispatch policy: greedy | batch | region-sharded")
	batchWindow := flag.Duration("batch-window", 3*time.Second, "batch window for batch and region-sharded")
	surgePremium := flag.Float64("surge-premium", 0, "seconds of matching cost discounted per unit of surge above 1x (batch/region-sharded); 0 = surge is display-only")
	etaWeight := flag.Float64("eta-weight", 0, "weight on the learned ETA's predicted trip duration in batch matching cost (throughput bias); 0 = off. Needs --eta-model")
	busKind := flag.String("bus", "memory", "event bus implementation: memory | kafka")
	kafkaSeeds := flag.String("kafka-seeds", "localhost:9092", "comma-separated Kafka bootstrap brokers (only with --bus=kafka)")
	etaModelPath := flag.String("eta-model", "", "path to a trained ETA model (from cmd/train-eta); empty disables prediction")
	driverAcceptRate := flag.Float64("driver-accept-rate", 0, "P(driver accepts an assignment), 0 disables (always accept)")
	driverCancelRate := flag.Float64("driver-cancel-rate", 0, "per-tick P(driver cancels before pickup), 0 disables")
	speed := flag.Float64("speed", 1.0, "initial sim-speed multiplier (also adjustable live in the UI)")
	maxWait := flag.Duration("max-wait", 0, "abandon a request unmatched this long (rider gives up); 0 disables")
	flag.Parse()

	g := mustLoadGraph(*osmPath)
	log.Printf("graph: %d nodes, %d edges", g.NodeCount(), g.EdgeCount())

	if *policy != "greedy" && *policy != "batch" && *policy != "region-sharded" {
		log.Fatalf("unknown --policy %q", *policy)
	}

	var bus events.Bus
	switch *busKind {
	case "memory":
		bus = events.NewMemoryBus()
	case "kafka":
		kb, err := kafka.New(strings.Split(*kafkaSeeds, ","))
		if err != nil {
			log.Fatalf("kafka bus: %v", err)
		}
		defer kb.Close()
		bus = kb
		log.Printf("event bus: kafka (seeds=%s)", *kafkaSeeds)
	default:
		log.Fatalf("unknown --bus %q (want memory or kafka)", *busKind)
	}

	engine := simulation.NewEngineWithBus(simulation.Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         10.0,
		SpeedMultiplier:  *speed,
	}, bus)

	// Rank match candidates by straight-line ETA instead of routing each one;
	// the chosen driver is still routed with A*. The experiment runner routes
	// every candidate.
	engine.GetDispatcher().SetETAEstimate(func(from, to int) float64 {
		fn, err1 := g.GetNode(from)
		tn, err2 := g.GetNode(to)
		if err1 != nil || err2 != nil {
			return math.Inf(1)
		}
		const avgSpeedMPS = 11.0 // rough city driving speed
		return graph.EuclideanDistance(fn, tn) / avgSpeedMPS
	})

	if *maxWait > 0 {
		engine.GetDispatcher().SetMaxWait(*maxWait)
		log.Printf("rider abandonment: requests unmatched for %s leave the queue", *maxWait)
	}

	// With --bus=kafka the metrics-aggregator owns trip metrics; subscribing here
	// too would join its consumer group and take some of its partitions.
	if *busKind == "memory" {
		if err := promexport.SubscribeToBus(bus); err != nil {
			log.Fatalf("prometheus: %v", err)
		}
	}
	promexport.RegisterFleetGauges()

	switch *policy {
	case "batch":
		bp := dispatcher.NewBatchPolicy(*batchWindow)
		bp.SurgePremiumSeconds = *surgePremium
		bp.ETAWeight = *etaWeight
		engine.GetDispatcher().SetPolicy(bp)
		log.Printf("dispatch policy: batch (window=%s, surge-premium=%.0fs, eta-weight=%.2f)", *batchWindow, *surgePremium, *etaWeight)
	case "region-sharded":
		rp := dispatcher.NewRegionShardedBatchPolicy(*batchWindow)
		rp.SurgePremiumSeconds = *surgePremium
		rp.ETAWeight = *etaWeight
		engine.GetDispatcher().SetPolicy(rp)
		log.Printf("dispatch policy: region-sharded (window=%s, r5, surge-premium=%.0fs, eta-weight=%.2f)", *batchWindow, *surgePremium, *etaWeight)
	default:
		log.Println("dispatch policy: greedy")
	}

	scenarioName := "demo"
	seed := int64(0)
	var sc *scenario.Scenario
	if *scenarioPath != "" {
		loaded, err := scenario.Load(*scenarioPath)
		if err != nil {
			log.Fatalf("scenario: %v", err)
		}
		sc = loaded
		scenarioName, seed = sc.Name, sc.Seed
	}

	if *etaModelPath != "" {
		m, err := eta.Load(*etaModelPath)
		if err != nil {
			log.Fatalf("eta model: %v", err)
		}
		engine.GetDispatcher().SetETAModel(m)
		log.Printf("eta model: loaded %s (train R2=%.3f RMSE=%.0fs)", *etaModelPath, m.R2, m.RMSE)
	}

	if *driverAcceptRate > 0 || *driverCancelRate > 0 {
		engine.GetDispatcher().SetDriverBehavior(dispatcher.DriverBehavior{
			AcceptRate: *driverAcceptRate,
			CancelRate: *driverCancelRate,
		}, seed)
		log.Printf("driver behavior: accept=%.2f cancel=%.3f", *driverAcceptRate, *driverCancelRate)
	}

	engine.EnableSurge()

	engine.SetRunInfo(events.RunInfo{Scenario: scenarioName, Policy: *policy, Seed: seed})

	if sc != nil {
		log.Printf("scenario: %s (%s)", sc.Name, sc.Description)

		gen := scenario.NewGenerator(sc, g)
		engine.SetArrivalSource(gen)
		if !sc.StartTime.IsZero() {
			engine.SetStartTime(sc.StartTime)
		}

		for _, node := range gen.VehicleSpawnNodes(g) {
			engine.SpawnVehicle(node)
		}
		log.Printf("spawned %d vehicles", sc.Vehicles.Count)
	} else {
		runHardcodedDemo(engine, g)
	}

	go engine.Run()

	// With --bus=kafka the browser UI comes from cmd/live-view; the engine only
	// publishes events.
	if *busKind == "memory" {
		srv := server.NewServer(engine)
		log.Printf("listening on http://localhost%s", *addr)
		go func() {
			if err := srv.Start(*addr); err != nil {
				log.Fatalf("server: %v", err)
			}
		}()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("metrosim: shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	} else {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		ms := &http.Server{Addr: *metricsAddr, Handler: mux}
		go func() {
			if err := ms.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("metrics: %v", err)
			}
		}()
		log.Printf("kafka bus: fleet metrics on %s; run cmd/live-view for the map", *metricsAddr)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("metrosim: shutting down")
		_ = ms.Close()
	}
}

func mustLoadGraph(osmPath string) *graph.Graph {
	if osmPath != "" {
		log.Printf("loading OSM graph from %s", osmPath)
		g, err := osm.LoadPBF(osmPath)
		if err != nil {
			log.Fatalf("graph load: %v", err)
		}
		return g
	}
	log.Println("loading CSV test graph")
	g, err := graph.LoadGraphFromCSV(
		"data/graphs/test_nodes.csv",
		"data/graphs/test_edges.csv",
	)
	if err != nil {
		log.Fatalf("graph load: %v", err)
	}
	return g
}

// runHardcodedDemo seeds 30 vehicles and 5 requests when no scenario is given.
func runHardcodedDemo(engine *simulation.Engine, g *graph.Graph) {
	spawnNodes := make([]int, 0, 9)
	for id := range g.Nodes {
		spawnNodes = append(spawnNodes, id)
		if len(spawnNodes) >= 9 {
			break
		}
	}
	for i := 0; i < 30; i++ {
		v := engine.SpawnVehicle(spawnNodes[i%len(spawnNodes)])
		if i%3 == 0 {
			v.SetDestination(spawnNodes[(i+5)%len(spawnNodes)])
			_ = v.PlanRoute()
		}
	}
	for i := 0; i < 5; i++ {
		engine.SubmitRequest(&dispatcher.Request{
			ID:              i + 1,
			PickupNode:      spawnNodes[i],
			DestinationNode: spawnNodes[(i+4)%len(spawnNodes)],
		})
	}
}
