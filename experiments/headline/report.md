# downtown: greedy vs batch as demand grows

| | |
|---|---|
| graph | `data/osm/city.osm.pbf`, 240128 nodes, 440814 edges |
| scenario | `scenarios/downtown.yaml`, 60 min, 180 vehicles, arrival rates times 1x, 1.25x, 1.5x, 1.75x, 2x |
| seeds | 1 to 10, each run once per policy and demand |
| command | `go run ./cmd/experiment --osm data/osm/city.osm.pbf --scenario scenarios/downtown.yaml --replicates 10 --parallel 8 --demand 1,1.25,1.5,1.75,2` |
| commit | `e5c6f5f` |

## Mean wait by demand

![mean wait by demand](demand.svg)

| demand | requests per run | arrivals per 3 s window | greedy mean wait (s) | batch mean wait (s) | batch minus greedy (s) | 95% CI | batch lower in |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1x | 426 | 0.35 | 112.4 | 113.8 | +1.4 | [+1.1, +1.7] | 0 of 10 |
| 1.25x | 529 | 0.44 | 132.9 | 134.0 | +1.1 | [+0.6, +1.6] | 1 of 10 |
| 1.5x | 639 | 0.53 | 167.6 | 168.4 | +0.8 | [-1.9, +3.4] | 2 of 10 |
| 1.75x | 748 | 0.62 | 221.7 | 215.3 | -6.2 | [-11.8, -0.6] | 8 of 10 |
| 2x | 857 | 0.71 | 359.0 | 254.4 | -103.0 | [-157.0, -48.9] | 10 of 10 |

Differences are per seed, paired, with a 95% t-interval across seeds. The sections below have each level in full.

## 1x demand

### All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 4259 | 4259 (100.0%) | 4240 | 112.4 | 77.3 | 303.2 |
| batch | 4259 | 4259 (100.0%) | 4239 | 113.8 | 78.9 | 305.1 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

### batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 111.6 | 112.7 | +1.1 |
| 2 | 116.3 | 117.3 | +1.0 |
| 3 | 123.2 | 124.4 | +1.2 |
| 4 | 135.5 | 137.2 | +1.6 |
| 5 | 118.8 | 121.0 | +2.2 |
| 6 | 99.4 | 101.2 | +1.8 |
| 7 | 101.4 | 102.3 | +0.9 |
| 8 | 110.5 | 111.7 | +1.2 |
| 9 | 109.1 | 110.5 | +1.4 |
| 10 | 99.3 | 101.0 | +1.8 |

Mean difference, batch minus greedy: +1.4 s, 95% t-interval [+1.1, +1.7] across seeds.

batch had the lower mean wait in 0 of 10 seeds; two-sided sign test p = 0.00195.

### Wait-time CDF, all trips

![wait_cdf](wait_cdf_1x.svg)

## 1.25x demand

### All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 5288 | 5288 (100.0%) | 5259 | 132.9 | 98.6 | 356.0 |
| batch | 5288 | 5288 (100.0%) | 5259 | 134.0 | 99.0 | 350.6 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

### batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 131.7 | 133.0 | +1.3 |
| 2 | 131.6 | 132.9 | +1.4 |
| 3 | 140.1 | 142.2 | +2.0 |
| 4 | 158.2 | 158.1 | -0.1 |
| 5 | 127.3 | 128.8 | +1.5 |
| 6 | 119.7 | 120.3 | +0.6 |
| 7 | 129.9 | 131.0 | +1.0 |
| 8 | 138.3 | 139.9 | +1.6 |
| 9 | 127.3 | 127.6 | +0.3 |
| 10 | 125.3 | 126.6 | +1.3 |

Mean difference, batch minus greedy: +1.1 s, 95% t-interval [+0.6, +1.6] across seeds.

batch had the lower mean wait in 1 of 10 seeds; two-sided sign test p = 0.0215.

### Wait-time CDF, all trips

![wait_cdf](wait_cdf_1.25x.svg)

## 1.5x demand

### All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 6387 | 6387 (100.0%) | 6345 | 167.6 | 125.6 | 499.5 |
| batch | 6387 | 6387 (100.0%) | 6345 | 168.4 | 126.4 | 501.0 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

### batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 155.5 | 157.0 | +1.5 |
| 2 | 145.3 | 145.5 | +0.3 |
| 3 | 185.5 | 186.3 | +0.7 |
| 4 | 218.9 | 211.7 | -7.2 |
| 5 | 169.3 | 169.9 | +0.6 |
| 6 | 156.4 | 163.2 | +6.8 |
| 7 | 176.4 | 177.2 | +0.8 |
| 8 | 158.4 | 157.1 | -1.4 |
| 9 | 146.2 | 146.9 | +0.7 |
| 10 | 160.0 | 165.1 | +5.1 |

Mean difference, batch minus greedy: +0.8 s, 95% t-interval [-1.9, +3.4] across seeds.

batch had the lower mean wait in 2 of 10 seeds; two-sided sign test p = 0.109.

### Wait-time CDF, all trips

![wait_cdf](wait_cdf_1.5x.svg)

## 1.75x demand

### All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 7476 | 7475 (100.0%) | 7408 | 221.7 | 154.9 | 699.3 |
| batch | 7476 | 7475 (100.0%) | 7416 | 215.3 | 150.6 | 664.1 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

### batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 222.3 | 203.7 | -18.6 |
| 2 | 174.7 | 174.1 | -0.6 |
| 3 | 263.3 | 244.3 | -19.0 |
| 4 | 259.7 | 249.7 | -10.0 |
| 5 | 217.5 | 215.3 | -2.2 |
| 6 | 242.9 | 234.1 | -8.8 |
| 7 | 210.2 | 210.6 | +0.3 |
| 8 | 201.5 | 197.4 | -4.1 |
| 9 | 186.6 | 182.8 | -3.8 |
| 10 | 230.6 | 235.1 | +4.4 |

Mean difference, batch minus greedy: -6.2 s, 95% t-interval [-11.8, -0.6] across seeds.

batch had the lower mean wait in 8 of 10 seeds; two-sided sign test p = 0.109.

### Wait-time CDF, all trips

![wait_cdf](wait_cdf_1.75x.svg)

## 2x demand

### All trips

| policy | requested | picked up | completed | mean wait (s) | p50 (s) | p95 (s) |
|---|---:|---:|---:|---:|---:|---:|
| greedy | 8570 | 8551 (99.8%) | 8381 | 359.0 | 231.4 | 1104.7 |
| batch | 8570 | 8566 (100.0%) | 8476 | 254.4 | 170.4 | 783.2 |

Waits cover every rider who asked for a ride, including riders still aboard when the run stopped. A rider never picked up counts with the time they had waited by then, which understates their wait.

### batch vs greedy: mean wait per seed (s)

| seed | greedy | batch | difference |
|---:|---:|---:|---:|
| 1 | 375.7 | 248.6 | -127.1 |
| 2 | 269.8 | 245.7 | -24.1 |
| 3 | 461.6 | 279.9 | -181.7 |
| 4 | 528.3 | 293.5 | -234.8 |
| 5 | 389.8 | 260.3 | -129.5 |
| 6 | 321.2 | 236.6 | -84.6 |
| 7 | 347.1 | 254.0 | -93.1 |
| 8 | 249.1 | 243.2 | -5.8 |
| 9 | 238.3 | 230.1 | -8.2 |
| 10 | 389.6 | 248.6 | -141.0 |

Mean difference, batch minus greedy: -103.0 s, 95% t-interval [-157.0, -48.9] across seeds.

batch had the lower mean wait in 10 of 10 seeds; two-sided sign test p = 0.00195.

### Wait-time CDF, all trips

![wait_cdf](wait_cdf_2x.svg)
