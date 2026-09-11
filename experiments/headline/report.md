# downtown: greedy vs batch

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles |
| seeds | 1 to 10, each run once per policy |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10` |
| commit | `033aee8` |

## All trips

| policy | requested | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|
| greedy | 4259 | 4218 (99.0%) | 139.0 | 97.8 | 377.7 |
| batch | 4259 | 4221 (99.1%) | 124.5 | 89.1 | 324.5 |

## batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 140.1 | 120.3 | -19.8 |
| 2 | 149.5 | 134.3 | -15.2 |
| 3 | 149.0 | 132.6 | -16.4 |
| 4 | 156.0 | 143.1 | -12.9 |
| 5 | 143.9 | 129.0 | -14.9 |
| 6 | 127.5 | 114.5 | -13.0 |
| 7 | 131.4 | 117.8 | -13.6 |
| 8 | 129.2 | 119.6 | -9.6 |
| 9 | 136.5 | 119.5 | -16.9 |
| 10 | 127.4 | 115.3 | -12.0 |

Mean difference, batch minus greedy: -14.4 s, 95% bootstrap CI [-16.2, -12.8] across seeds.

batch had the lower mean wait in 10 of 10 seeds; two-sided sign test p = 0.00195.

## Wait-time CDF, all trips

![wait_cdf](wait_cdf.svg)
