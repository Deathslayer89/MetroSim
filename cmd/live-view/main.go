// Command live-view consumes driver, surge, and trip events from Kafka and
// fans them out to browser websocket clients. The static frontend (Leaflet
// map + controls) is served from web/static.
//
//	make stack-up
//	go run ./cmd/live-view --kafka-seeds=localhost:9092 --addr=:8080
//	open http://localhost:8080
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Deathslayer89/MetroSim/internal/events/kafka"
	"github.com/Deathslayer89/MetroSim/internal/liveview"
)

func main() {
	seeds := flag.String("kafka-seeds", "localhost:9092", "comma-separated Kafka brokers")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	staticDir := flag.String("static", "web/static", "directory of static frontend files")
	flag.Parse()

	bus, err := kafka.New(strings.Split(*seeds, ","))
	if err != nil {
		log.Fatalf("kafka: %v", err)
	}

	state := liveview.NewState()
	if err := state.SubscribeToBus(bus); err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	srv := liveview.NewServer(state, *staticDir)
	mux := http.NewServeMux()
	srv.Routes(mux)
	mux.Handle("/metrics", promhttp.Handler())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Broadcaster(ctx)

	server := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		log.Printf("live-view: serving %s from %s, consuming from %s", *addr, *staticDir, *seeds)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("live-view: shutting down")
	cancel()
	bus.Close()
	_ = server.Close()
}
