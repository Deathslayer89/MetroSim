// Command replay rewinds a Kafka consumer group's committed offsets so that
// downstream services (trace-writer, metrics-aggregator, live-view) re-consume
// historical events. The log is the source of truth; replay only moves the
// group's offsets.
//
// Workflow:
//
//	docker compose -f deploy/docker-compose.yml stop trace-writer
//	go run ./cmd/replay --group=trace-writer --from=earliest
//	docker compose -f deploy/docker-compose.yml start trace-writer
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

func main() {
	seeds := flag.String("kafka-seeds", "localhost:9092", "comma-separated Kafka brokers")
	group := flag.String("group", "", "consumer group to rewind (required)")
	from := flag.String("from", "earliest", "earliest | latest | RFC3339 timestamp (e.g. 2026-03-02T17:00:00Z)")
	dryRun := flag.Bool("dry-run", false, "print the planned offset diff and exit without committing")
	flag.Parse()

	if *group == "" {
		fmt.Fprintln(os.Stderr, "--group is required")
		flag.Usage()
		os.Exit(2)
	}

	client, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(*seeds, ",")...))
	if err != nil {
		log.Fatalf("kafka: %v", err)
	}
	defer client.Close()
	adm := kadm.NewClient(client)
	ctx := context.Background()

	committed, err := adm.FetchOffsets(ctx, *group)
	if err != nil {
		log.Fatalf("fetch offsets for group %q: %v", *group, err)
	}
	topics := make(map[string]struct{})
	committed.Each(func(o kadm.OffsetResponse) {
		topics[o.Topic] = struct{}{}
	})
	if len(topics) == 0 {
		log.Fatalf("consumer group %q has no committed offsets; nothing to rewind", *group)
	}
	topicList := make([]string, 0, len(topics))
	for t := range topics {
		topicList = append(topicList, t)
	}

	target, err := resolveOffsets(ctx, adm, *from, topicList)
	if err != nil {
		log.Fatalf("resolve --from=%s: %v", *from, err)
	}

	verb := "rewinding"
	if *dryRun {
		verb = "dry-run: would rewind"
	}
	log.Printf("%s consumer group %q across %d topics", verb, *group, len(topicList))
	target.Each(func(o kadm.Offset) {
		var beforeAt int64 = -1
		if before, ok := committed.Lookup(o.Topic, o.Partition); ok {
			beforeAt = before.At
		}
		log.Printf("  %s p%d: offset %d -> %d", o.Topic, o.Partition, beforeAt, o.At)
	})

	if *dryRun {
		log.Printf("dry-run: nothing committed. re-run without --dry-run to apply.")
		return
	}

	if err := adm.CommitAllOffsets(ctx, *group, target); err != nil {
		log.Fatalf("commit offsets: %v (consumers still active? stop them first)", err)
	}
	log.Printf("rewind committed. start the consumer(s) to re-process from new offsets.")
}

func resolveOffsets(ctx context.Context, adm *kadm.Client, from string, topics []string) (kadm.Offsets, error) {
	switch from {
	case "earliest":
		ls, err := adm.ListStartOffsets(ctx, topics...)
		if err != nil {
			return nil, err
		}
		return listedToOffsets(ls), nil
	case "latest":
		ls, err := adm.ListEndOffsets(ctx, topics...)
		if err != nil {
			return nil, err
		}
		return listedToOffsets(ls), nil
	}

	t, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return nil, fmt.Errorf("not earliest|latest and not an RFC3339 timestamp: %w", err)
	}
	ls, err := adm.ListOffsetsAfterMilli(ctx, t.UnixMilli(), topics...)
	if err != nil {
		return nil, err
	}
	return listedToOffsets(ls), nil
}

// listedToOffsets converts a ListedOffsets result into the Offsets-to-commit
// shape. Partitions that came back with a non-nil Err are skipped: treating
// them as offset 0 would wipe a partition the broker was briefly unreachable for.
func listedToOffsets(ls kadm.ListedOffsets) kadm.Offsets {
	out := make(kadm.Offsets)
	ls.Each(func(o kadm.ListedOffset) {
		if o.Err != nil {
			log.Printf("warning: %s partition %d: list failed, not resetting it: %v", o.Topic, o.Partition, o.Err)
			return
		}
		out.AddOffset(o.Topic, o.Partition, o.Offset, -1)
	})
	return out
}
