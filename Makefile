.PHONY: help build test test-race vet lint demo experiment fetch-osm train-eta \
	kafka-up kafka-down kafka-test trace-writer metrics-aggregator live-view replay-trace-writer \
	stack-up stack-down stack-logs cluster-up cluster-down clean

GO ?= go
OSM ?= data/osm/city.osm.pbf
COMPOSE ?= docker compose

help:
	@echo "Targets:"
	@echo "  build               Build every command into bin/"
	@echo "  test                Run unit tests"
	@echo "  test-race           Run unit tests under the race detector"
	@echo "  vet                 go vet ./..."
	@echo "  lint                Run staticcheck, installing it if missing"
	@echo "  demo                Quick A/B run on the CSV test grid"
	@echo "  experiment          The README's headline A/B run on San Francisco (needs fetch-osm)"
	@echo "  fetch-osm           Download the San Francisco OSM extract (~30 MB)"
	@echo "  train-eta           Fit models/eta.json on routed pairs from the SF graph"
	@echo "  kafka-up            Start the Kafka broker on its own"
	@echo "  kafka-down          Stop the Kafka broker"
	@echo "  kafka-test          Run the Kafka integration test against the broker"
	@echo "  trace-writer        Run trace-writer against localhost:9092"
	@echo "  metrics-aggregator  Run a metrics-aggregator on :9101"
	@echo "  live-view           Run live-view on :8080"
	@echo "  replay-trace-writer Rewind the trace-writer group to the earliest offset"
	@echo "  stack-up            Build and start Kafka, trace-writer, two metrics-aggregators, live-view, Prometheus and Grafana"
	@echo "  stack-down          Stop the stack"
	@echo "  stack-logs          Follow every service's logs"
	@echo "  cluster-up          Start the 3-broker cluster"
	@echo "  cluster-down        Stop the 3-broker cluster"
	@echo "  clean               Remove bin/"

build:
	$(GO) build -o bin/ ./cmd/...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	@which staticcheck >/dev/null 2>&1 || $(GO) install honnef.co/go/tools/cmd/staticcheck@v0.8.1
	staticcheck ./...

demo:
	$(GO) run ./cmd/experiment --scenario scenarios/smoke.yaml --replicates 2

experiment:
	$(GO) run ./cmd/experiment --osm $(OSM) --scenario scenarios/downtown.yaml --replicates 10

fetch-osm:
	bash data/osm/fetch.sh

train-eta:
	$(GO) run ./cmd/train-eta --sample-osm=$(OSM) --samples=2000 --out=models/eta.json

kafka-up:
	$(COMPOSE) -f deploy/docker-compose.yml up -d kafka

kafka-down:
	$(COMPOSE) -f deploy/docker-compose.yml stop kafka

kafka-test:
	$(GO) test -race -tags=integration ./internal/events/kafka/...

trace-writer:
	$(GO) run ./cmd/trace-writer --kafka-seeds=localhost:9092 --out-dir=traces

metrics-aggregator:
	$(GO) run ./cmd/metrics-aggregator --kafka-seeds=localhost:9092 --addr=:9101

live-view:
	$(GO) run ./cmd/live-view --kafka-seeds=localhost:9092 --addr=:8080 --static=web/static

# Stop trace-writer first; Kafka won't reset offsets for a group with members.
replay-trace-writer:
	$(GO) run ./cmd/replay --group=trace-writer --from=earliest

# HOST_UID and HOST_GID let the trace-writer container write to ./traces.
stack-up:
	mkdir -p traces
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE) -f deploy/docker-compose.yml up -d --build

stack-down:
	$(COMPOSE) -f deploy/docker-compose.yml down

stack-logs:
	$(COMPOSE) -f deploy/docker-compose.yml logs -f

cluster-up:
	mkdir -p traces
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) $(COMPOSE) -f deploy/docker-compose.cluster.yml up -d --build

cluster-down:
	$(COMPOSE) -f deploy/docker-compose.cluster.yml down

clean:
	rm -rf bin
