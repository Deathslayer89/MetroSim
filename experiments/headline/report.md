# downtown: greedy vs batch

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles |
| seeds | 1 to 10, each run once per policy |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10` |
| commit | `9e2df3b` |

## All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 4259 | 4259 (100.0%) | 4219 | 132.3 | 90.6 | 365.3 |
| batch | 4259 | 4259 (100.0%) | 4219 | 133.5 | 92.4 | 368.8 |

Waits cover every rider who was picked up, including riders still aboard when the run stopped.

## batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 131.3 | 132.2 | +0.9 |
| 2 | 135.7 | 137.1 | +1.4 |
| 3 | 141.7 | 143.4 | +1.8 |
| 4 | 157.4 | 158.5 | +1.0 |
| 5 | 137.5 | 139.2 | +1.7 |
| 6 | 118.9 | 119.7 | +0.8 |
| 7 | 124.4 | 125.7 | +1.3 |
| 8 | 130.6 | 132.1 | +1.4 |
| 9 | 128.2 | 129.6 | +1.4 |
| 10 | 117.7 | 118.4 | +0.6 |

Mean difference, batch minus greedy: +1.2 s, 95% bootstrap CI [+1.0, +1.5] across seeds.

batch had the lower mean wait in 0 of 10 seeds; two-sided sign test p = 0.00195.

## Wait-time CDF, all trips

![wait_cdf](wait_cdf.svg)
