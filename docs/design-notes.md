# MetroSim design notes

## Statistics

The seed is the unit, not the trip. Trips in one run share drivers and roads, and treating thousands of them as independent samples overstates what ten runs can show.

Intervals on the per-seed differences come from Student's t with nine degrees of freedom, 2.26 standard errors either side. The first reports used a percentile bootstrap, which at ten samples stops near 1.96 and misses the true mean more often than it claims; a test draws 4,000 samples of ten and checks the t-interval covers the mean 94 to 96% of the time. The sign test next to it assumes nothing about the distribution. It sums binomial terms in log space: the first version computed 2^n directly and returned NaN past about 1,000 seeds.

## Loading the graph

Kosaraju runs iteratively rather than recursing up to 256,000 nodes deep. Node IDs follow sorted OSM IDs, because Go's map order changes between runs and the whole simulation with it. Lanes are per direction: `lanes:forward` and `lanes:backward` when tagged, an even split of `lanes` otherwise.

## Routing

Weighted A* keeps its 3x bound only if the planner re-expands a node when it finds a cheaper way in, which it does. The price of the weight shows up near the goal, where a jammed direct edge has to get very slow before the search tries a detour. The rerouting test piles 100 cars on one edge before the car takes the side streets.

`TestMetersHeuristicTakesStraightRoute` pins the classic mistake, a heuristic in meters against costs in seconds, which picks a slow straight road over a fast detour.

## Replanning

A car replans at most once a simulated second, and only when an edge still ahead of it changed by more than 10%. It switches only for a route at least 10% cheaper; without that margin, routes flap. Changes count from the last report rather than the last tick, or a jam that grows a little every tick would never be reported. The traffic maps hold occupied links only, and a tick costs time in proportion to the fleet, not the 440,814 edges.

## Matching

The Hungarian solver pads the cost matrix to square with a large cost that means unmatched. That padding is the only reason the surge premium and the ETA weight do anything. Each adds the same amount to every candidate of a request, which changes nothing on a square matrix of real costs, but the padding cells don't get it. It shifts the cost of matching a request against leaving it unmatched, and with more requests than drivers it decides who gets the contested drivers. `eta_weight_test.go` has the two-request case: weight 0 picks the closer pickup, weight 1 the shorter trip.

Two regions of the sharded variant can see the same driver near their boundary. Regions solve in sorted order and each removes the drivers it assigns from the shared index; `TestRegionShardedBoundaryDriverAssignedOnce` fails without that removal.

Nearest-driver search looks eight H3 rings out and falls back to every idle driver when that finds fewer than it needs, mostly when nearby cars are busy.

A driver who declines or cancels isn't offered that request again. Cancellation is a rate per simulated minute; as a chance per tick, `--speed` changed how often drivers cancelled. A request with no route from pickup to dropoff is turned away before any driver is sent.

Repositioning wants the target cell at least two cars shorter than the car's own. With a lead of one, the move just swaps the two shortfalls, and the next pass sends the car back.

## Determinism

Arrivals and driver behavior draw from separate RNGs. Seeded with the same run seed, behavior repeated the arrival draws, so its RNG gets the seed XORed with a constant. Rides iterate in ID order, nearest-driver ties break on driver ID, and links are built in edge-ID order. `TestEngineRunIsDeterministic` runs the whole engine twice from one seed and compares every wait.

## Kafka

Partition keys are per trip, driver and cell, but each event type has its own topic, so a trip's events stay in order within a topic and not across topics. live-view only counts events, and counting doesn't care about order.

In one process, trip events go out after the engine lock is released. A MemoryBus subscriber runs on the publisher's goroutine, and one that read the engine used to deadlock.

A consumer group holds rebalances until the fetch in hand is committed. The price: a sink that keeps failing past the group's rebalance timeout gets its member dropped, and the batch moves to another member.

Delivery is at least once. trace-writer dedups on the run's labels, the ride ID and the trip's times, since ride IDs restart every run. The Prometheus counters would count a redelivered fetch twice.

## The map page

Each websocket client holds at most one pending frame, replaced by the next, and a slow client skips ahead instead of replaying stale ones. Cross-origin upgrades are refused; before that, any web page could send "stop".

## ETA model

The hour of day goes in as a sine and a cosine, so midnight isn't a cliff. A feature that never varies, like surge in a run without it, gets weight 0; it used to make the normal equations singular at lambda 0.
