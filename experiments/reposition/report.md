# downtown: batch vs batch+reposition

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles |
| seeds | 1 to 10, each run once per policy |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10 --parallel 8 --policies batch,batch+reposition` |
| commit | `e5c6f5f` |

## All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| batch | 4259 | 4259 (100.0%) | 4239 | 113.8 | 78.9 | 305.1 |
| batch+reposition | 4259 | 4259 (100.0%) | 4240 | 79.1 | 43.7 | 291.2 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

batch+reposition made 5999 repositioning moves, 600 per run, and drove 1347 km per run repositioning, 29.1% of all its driving.

## batch+reposition vs batch: mean wait per seed (s)

| seed | batch | batch+reposition | difference |
|---:|---:|---:|---:|
| 1 | 112.7 | 81.9 | -30.9 |
| 2 | 117.3 | 89.2 | -28.0 |
| 3 | 124.4 | 91.6 | -32.8 |
| 4 | 137.2 | 100.7 | -36.5 |
| 5 | 121.0 | 85.1 | -35.9 |
| 6 | 101.2 | 62.5 | -38.8 |
| 7 | 102.3 | 67.7 | -34.6 |
| 8 | 111.7 | 74.8 | -36.9 |
| 9 | 110.5 | 75.0 | -35.5 |
| 10 | 101.0 | 64.0 | -37.0 |

Mean difference, batch+reposition minus batch: -34.7 s, 95% t-interval [-37.0, -32.4] across seeds.

batch+reposition had the lower mean wait in 10 of 10 seeds; two-sided sign test p = 0.00195.

## Wait-time CDF, all trips

![wait_cdf](wait_cdf.svg)
