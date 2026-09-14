package dispatcher

import (
	"math"
	"sort"

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

// H3DriverIndex buckets idle drivers by H3 cell and answers NearestK via
// grid_disk over the query cell. Falls back to exhaustive scan if the disk is
// empty (sparse-area pickup).
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

	candidates := make([]DriverLocation, 0, 16)
	seen := make(map[int]struct{})
	disk, err := queryCell.GridDisk(candidateRings)
	if err != nil {
		return nil
	}
	for _, cell := range disk {
		for id := range idx.byCell[cell] {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			candidates = append(candidates, idx.locations[id])
		}
	}

	// Fewer than k within the rings, as when most cars nearby are busy: look at
	// every idle driver, or a request settles for the one or two it found.
	if len(candidates) < k {
		candidates = candidates[:0]
		for _, loc := range idx.locations {
			candidates = append(candidates, loc)
		}
		if len(candidates) == 0 {
			return nil
		}
	}

	// Break distance ties on driver ID so map order can't change the result.
	sort.Slice(candidates, func(i, j int) bool {
		di := squaredDistDeg(lat, lon, candidates[i].Lat, candidates[i].Lon)
		dj := squaredDistDeg(lat, lon, candidates[j].Lat, candidates[j].Lon)
		if di != dj {
			return di < dj
		}
		return candidates[i].ID < candidates[j].ID
	})

	if k > len(candidates) {
		k = len(candidates)
	}
	out := make([]*DriverLocation, k)
	for i := 0; i < k; i++ {
		out[i] = &candidates[i]
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
