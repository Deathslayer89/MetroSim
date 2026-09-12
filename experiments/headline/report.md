# downtown: greedy vs batch

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles |
| seeds | 1 to 10, each run once per policy |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10` |
| commit | `feeaba2` |

## All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 4259 | 4258 (100.0%) | 4214 | 149.5 | 100.8 | 415.5 |
| batch | 4259 | 4259 (100.0%) | 4219 | 133.5 | 92.4 | 368.8 |

Waits cover every rider who was picked up, including riders still aboard when the run stopped.

## batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 151.7 | 132.2 | -19.5 |
| 2 | 155.1 | 137.1 | -18.0 |
| 3 | 165.6 | 143.4 | -22.2 |
| 4 | 172.6 | 158.5 | -14.2 |
| 5 | 158.5 | 139.2 | -19.3 |
| 6 | 135.8 | 119.7 | -16.1 |
| 7 | 137.0 | 125.7 | -11.2 |
| 8 | 140.9 | 132.1 | -8.8 |
| 9 | 148.3 | 129.6 | -18.7 |
| 10 | 130.0 | 118.4 | -11.6 |

Mean difference, batch minus greedy: -16.0 s, 95% bootstrap CI [-18.5, -13.3] across seeds.

batch had the lower mean wait in 10 of 10 seeds; two-sided sign test p = 0.00195.

## Wait-time CDF, all trips

![wait_cdf](wait_cdf.svg)
