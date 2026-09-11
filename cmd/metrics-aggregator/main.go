// Command metrics-aggregator consumes trip events from Kafka and serves the
// wait and duration histograms and the matched counter to Prometheus.
// Replicas share one consumer group, so each sees some of the partitions and
// Prometheus sums across them.
//
//	go run ./cmd/metrics-aggregator --kafka-seeds=localhost:9092 --addr=:9100
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Deathslayer89/MetroSim/internal/events/kafka"
	"github.com/Deathslayer89/MetroSim/internal/promexport"
)

func main() {
	seeds := flag.String("kafka-seeds", "localhost:9092", "comma-separated Kafka brokers")
	addr := flag.String("addr", ":9100", "Prometheus scrape address")
	flag.Parse()

	bus, err := kafka.New(strings.Split(*seeds, ","))
	if err != nil {
		log.Fatalf("kafka: %v", err)
	}

	if err := promexport.SubscribeToBus(bus); err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		log.Printf("metrics-aggregator: serving Prometheus on %s, consuming from %s", *addr, *seeds)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("metrics-aggregator: shutting down")

	bus.Close()
	_ = server.Close()
}
