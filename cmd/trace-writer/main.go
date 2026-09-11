// Command trace-writer consumes trip.completed from Kafka and writes one
// Parquet file per consumed batch under --out-dir. Offsets commit only after a
// batch's file is complete, and trips already on disk are skipped when Kafka
// redelivers them.
//
//	go run ./cmd/trace-writer --kafka-seeds=localhost:9092 --out-dir=traces
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Deathslayer89/MetroSim/internal/events/kafka"
	"github.com/Deathslayer89/MetroSim/internal/tracelog"
)

func main() {
	seeds := flag.String("kafka-seeds", "localhost:9092", "comma-separated Kafka brokers")
	outDir := flag.String("out-dir", "traces", "directory for rolled Parquet files")
	flag.Parse()

	bus, err := kafka.New(strings.Split(*seeds, ","))
	if err != nil {
		log.Fatalf("kafka: %v", err)
	}
	rec, err := tracelog.NewRollingRecorder(*outDir)
	if err != nil {
		log.Fatalf("recorder: %v", err)
	}
	if err := rec.SubscribeToBus(bus); err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	log.Printf("trace-writer: consuming trip.completed from %s, writing to %s", *seeds, *outDir)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("trace-writer: shutting down")

	// Close the bus first: it waits for handlers that may still be calling
	// rec.Record.
	bus.Close()
	if err := rec.Close(); err != nil {
		log.Printf("trace-writer: close: %v", err)
	}
}
