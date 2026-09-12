# downtown: batch vs batch+reposition

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles |
| seeds | 1 to 10, each run once per policy |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10 --parallel 8 --policies batch,batch+reposition` |
| commit | `7d415a5` |

## All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| batch | 4259 | 4259 (100.0%) | 4238 | 113.8 | 78.7 | 305.1 |
| batch+reposition | 4259 | 4259 (100.0%) | 4240 | 79.1 | 43.7 | 291.2 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

batch+reposition made 5998 repositioning moves, 600 per run.

## batch+reposition vs batch: mean wait per seed (s)

| seed | batch | batch+reposition | difference |
|---:|---:|---:|---:|
| 1 | 112.7 | 81.9 | -30.9 |
| 2 | 117.3 | 89.2 | -28.0 |
| 3 | 124.4 | 91.6 | -32.8 |
| 4 | 137.2 | 98.7 | -38.5 |
| 5 | 120.6 | 85.3 | -35.3 |
| 6 | 102.0 | 62.9 | -39.1 |
| 7 | 102.3 | 67.7 | -34.6 |
| 8 | 112.0 | 75.1 | -36.9 |
| 9 | 110.5 | 75.0 | -35.5 |
| 10 | 100.3 | 64.1 | -36.1 |

Mean difference, batch+reposition minus batch: -34.8 s, 95% bootstrap CI [-36.6, -32.6] across seeds.

batch+reposition had the lower mean wait in 10 of 10 seeds; two-sided sign test p = 0.00195.

## Wait-time CDF, all trips

![wait_cdf](wait_cdf.svg)
