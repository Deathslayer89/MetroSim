package dispatcher

import (
	"math"

	"github.com/uber/h3-go/v4"
)

// DriverLocation is a driver's last-known position, returned by NearestK.
type DriverLocation struct {
	ID  int
	Lat float64
	Lon float64
}

// driverResolution is H3 r9, cells of about 0.1 square kilometers. Eight rings reach
// roughly 2.2 km from a pickup.
const (
	driverResolution = 9
	candidateRings   = 8
)

/**
 * H3DriverIndex buckets idle drivers by H3 cell and answers NearestK via
 * grid_disk over the query cell. Falls back to every idle driver when the disk
 * holds fewer than k.
 */
type H3DriverIndex struct {
	byCell     map[h3.Cell]map[int]struct{}
	driverCell map[int]h3.Cell
	locations  map[int]DriverLocation
}

func NewH3DriverIndex() *H3DriverIndex {
	return &H3DriverIndex{
		byCell:     make(map[h3.Cell]map[int]struct{}),
		driverCell: make(map[int]h3.Cell),
		locations:  make(map[int]DriverLocation),
	}
}

func (idx *H3DriverIndex) Insert(vehicleID int, lat, lon float64) {
	cell, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lon}, driverResolution)
	if err != nil {
		return
	}
	if _, ok := idx.byCell[cell]; !ok {
		idx.byCell[cell] = make(map[int]struct{})
	}
	idx.byCell[cell][vehicleID] = struct{}{}
	idx.driverCell[vehicleID] = cell
	idx.locations[vehicleID] = DriverLocation{ID: vehicleID, Lat: lat, Lon: lon}
}

func (idx *H3DriverIndex) Remove(vehicleID int) {
	cell, ok := idx.driverCell[vehicleID]
	if !ok {
		return
	}
	if drivers := idx.byCell[cell]; drivers != nil {
		delete(drivers, vehicleID)
		if len(drivers) == 0 {
			delete(idx.byCell, cell)
		}
	}
	delete(idx.driverCell, vehicleID)
	delete(idx.locations, vehicleID)
}

func (idx *H3DriverIndex) Clear() {
	idx.byCell = make(map[h3.Cell]map[int]struct{})
	idx.driverCell = make(map[int]h3.Cell)
	idx.locations = make(map[int]DriverLocation)
}

func (idx *H3DriverIndex) Count() int {
	return len(idx.locations)
}

func (idx *H3DriverIndex) NearestK(lat, lon float64, k int) []*DriverLocation {
	queryCell, err := h3.LatLngToCell(h3.LatLng{Lat: lat, Lng: lon}, driverResolution)
	if err != nil {
		return nil
	}

	best := newNearest(lat, lon, k)
	disk, err := queryCell.GridDisk(candidateRings)
	if err != nil {
		return nil
	}
	found := 0
	for _, cell := range disk {
		for id := range idx.byCell[cell] {
			best.offer(idx.locations[id])
			found++
		}
	}

	// Fewer than k within the rings, as when most cars nearby are busy: look at
	// every idle driver, or a request settles for the one or two it found.
	if found < k {
		best = newNearest(lat, lon, k)
		for _, loc := range idx.locations {
			best.offer(loc)
		}
	}
	return best.result()
}

/**
 * nearest keeps the k closest drivers offered so far, sorted, in O(k) per offer,
 * so the full-fleet fallback costs O(n*k) rather than a sort of every idle driver.
 * Distance ties break on driver ID so map order can't change the result.
 */
type nearest struct {
	lat, lon float64
	k        int
	locs     []DriverLocation
	dists    []float64
}

func newNearest(lat, lon float64, k int) *nearest {
	return &nearest{lat: lat, lon: lon, k: k, locs: make([]DriverLocation, 0, k), dists: make([]float64, 0, k)}
}

func (n *nearest) offer(loc DriverLocation) {
	if n.k <= 0 {
		return
	}
	d := squaredDistDeg(n.lat, n.lon, loc.Lat, loc.Lon)
	closer := func(i int) bool {
		return d < n.dists[i] || (d == n.dists[i] && loc.ID < n.locs[i].ID)
	}
	if len(n.locs) == n.k && !closer(n.k-1) {
		return
	}
	if len(n.locs) < n.k {
		n.locs = append(n.locs, loc)
		n.dists = append(n.dists, d)
	}
	i := len(n.locs) - 1
	for i > 0 && closer(i-1) {
		n.locs[i], n.dists[i] = n.locs[i-1], n.dists[i-1]
		i--
	}
	n.locs[i], n.dists[i] = loc, d
}

func (n *nearest) result() []*DriverLocation {
	if len(n.locs) == 0 {
		return nil
	}
	out := make([]*DriverLocation, len(n.locs))
	for i := range n.locs {
		out[i] = &n.locs[i]
	}
	return out
}

// CellsByDriverCount returns occupied cells and their driver counts. Snapshot;
// the returned map is safe to read after the caller releases the dispatcher lock.
func (idx *H3DriverIndex) CellsByDriverCount() map[h3.Cell]int {
	out := make(map[h3.Cell]int, len(idx.byCell))
	for cell, drivers := range idx.byCell {
		out[cell] = len(drivers)
	}
	return out
}

// squaredDistDeg ranks candidates by squared degree distance, with longitude
// scaled by cos(latitude) as in graph.EuclideanDistance.
func squaredDistDeg(lat1, lon1, lat2, lon2 float64) float64 {
	dlat := lat1 - lat2
	dlon := (lon1 - lon2) * math.Cos((lat1+lat2)/2*math.Pi/180)
	return dlat*dlat + dlon*dlon
}
