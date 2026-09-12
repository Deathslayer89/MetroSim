# downtown: batch vs batch+reposition

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles |
| seeds | 1 to 10, each run once per policy |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10 --policies batch,batch+reposition` |
| commit | `21f7a91` |

## All trips

| policy | requested | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|
| batch | 4259 | 4221 (99.1%) | 124.5 | 89.1 | 324.5 |
| batch+reposition | 4259 | 4229 (99.3%) | 86.5 | 50.0 | 312.1 |

## batch+reposition vs batch: mean wait per seed (s)

| seed | batch | batch+reposition | difference |
|---:|---:|---:|---:|
| 1 | 120.3 | 84.6 | -35.8 |
| 2 | 134.3 | 102.7 | -31.5 |
| 3 | 132.6 | 96.9 | -35.7 |
| 4 | 143.1 | 103.7 | -39.4 |
| 5 | 129.0 | 87.2 | -41.7 |
| 6 | 114.5 | 74.3 | -40.1 |
| 7 | 117.8 | 77.4 | -40.5 |
| 8 | 119.6 | 80.7 | -38.8 |
| 9 | 119.5 | 83.6 | -35.9 |
| 10 | 115.3 | 75.2 | -40.1 |

Mean difference, batch+reposition minus batch: -38.0 s, 95% bootstrap CI [-39.7, -36.0] across seeds.

batch+reposition had the lower mean wait in 10 of 10 seeds; two-sided sign test p = 0.00195.

## Wait-time CDF, all trips

![wait_cdf](wait_cdf.svg)
