// Package osm loads a drivable road graph from an OpenStreetMap .osm.pbf file.
package osm

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	pmosm "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"github.com/Deathslayer89/MetroSim/internal/graph"
)

// fallbackSpeed returns free-flow m/s for a highway= tag, or 0 if non-drivable.
func fallbackSpeed(highway string) float64 {
	switch highway {
	case "motorway", "motorway_link":
		return 27.78
	case "trunk", "trunk_link":
		return 22.22
	case "primary", "primary_link":
		return 16.67
	case "secondary", "secondary_link":
		return 13.89
	case "tertiary", "tertiary_link":
		return 11.11
	case "unclassified", "residential", "living_street":
		return 8.33
	case "service":
		return 5.56
	default:
		return 0
	}
}

// parseMaxspeed converts an OSM maxspeed value to m/s. Returns 0 if absent or
// non-numeric ("none", "signals", "RU:urban", etc.) so the caller falls back.
func parseMaxspeed(s string) float64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "none" || s == "signals" {
		return 0
	}
	mph := false
	switch {
	case strings.HasSuffix(s, " mph"):
		mph = true
		s = strings.TrimSuffix(s, " mph")
	case strings.HasSuffix(s, "mph"):
		mph = true
		s = strings.TrimSuffix(s, "mph")
	case strings.HasSuffix(s, "km/h"):
		s = strings.TrimSuffix(s, "km/h")
	case strings.HasSuffix(s, "kph"):
		s = strings.TrimSuffix(s, "kph")
	case strings.HasSuffix(s, "kmh"):
		s = strings.TrimSuffix(s, "kmh")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return 0
	}
	if mph {
		return v * 0.44704
	}
	return v / 3.6
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusM = 6371000.0
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dlat := (lat2 - lat1) * math.Pi / 180
	dlon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return earthRadiusM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

type wayRec struct {
	nodes      []pmosm.NodeID
	speedLimit float64
	lanes      int  // lanes along the stored direction
	lanesBack  int  // lanes against it; unused on one-way roads
	oneway     bool // travel only in stored direction (after reverse normalization)
}

// LoadPBF parses path and returns a directed driving graph. Highway=* ways are
// kept; consecutive node pairs become edges. Oneway is honoured (oneway=-1
// reverses the way before edge emission).
func LoadPBF(path string) (*graph.Graph, error) {
	ways, err := scanWays(path)
	if err != nil {
		return nil, fmt.Errorf("scan ways: %w", err)
	}
	if len(ways) == 0 {
		return nil, fmt.Errorf("no drivable highway ways found in %s", path)
	}

	needed := make(map[pmosm.NodeID]struct{}, len(ways)*8)
	for _, w := range ways {
		for _, n := range w.nodes {
			needed[n] = struct{}{}
		}
	}

	nodes, err := scanNodes(path, needed)
	if err != nil {
		return nil, fmt.Errorf("scan nodes: %w", err)
	}

	g, err := assemble(ways, nodes)
	if err != nil {
		return nil, err
	}
	// A raw extract has pairs with no route between them, so keep only the
	// largest strongly connected component.
	if removed := g.KeepLargestSCC(); removed > 0 {
		log.Printf("osm: pruned %d nodes outside the largest SCC (%d remain)", removed, g.NodeCount())
	}
	return g, nil
}

func scanWays(path string) ([]wayRec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := osmpbf.New(context.Background(), f, runtime.NumCPU())
	defer scanner.Close()
	scanner.SkipNodes = true
	scanner.SkipRelations = true

	var ways []wayRec
	for scanner.Scan() {
		w, ok := scanner.Object().(*pmosm.Way)
		if !ok {
			continue
		}
		highway := w.Tags.Find("highway")
		fb := fallbackSpeed(highway)
		if fb == 0 {
			continue
		}
		if w.Tags.Find("access") == "no" {
			continue
		}
		speed := parseMaxspeed(w.Tags.Find("maxspeed"))
		if speed == 0 {
			speed = fb
		}
		oneway := w.Tags.Find("oneway")
		isOneway := oneway == "yes" || oneway == "true" || oneway == "1" || oneway == "-1"
		// OSM treats motorways and roundabouts as one-way unless tagged otherwise.
		if oneway != "no" && oneway != "false" && oneway != "0" {
			junction := w.Tags.Find("junction")
			if highway == "motorway" || highway == "motorway_link" || junction == "roundabout" || junction == "circular" {
				isOneway = true
			}
		}

		lanes, lanesBack := wayLanes(w.Tags, isOneway)

		nodeIDs := make([]pmosm.NodeID, 0, len(w.Nodes))
		for _, n := range w.Nodes {
			nodeIDs = append(nodeIDs, n.ID)
		}
		if oneway == "-1" {
			for i, j := 0, len(nodeIDs)-1; i < j; i, j = i+1, j-1 {
				nodeIDs[i], nodeIDs[j] = nodeIDs[j], nodeIDs[i]
			}
		}

		ways = append(ways, wayRec{
			nodes:      nodeIDs,
			speedLimit: speed,
			lanes:      lanes,
			lanesBack:  lanesBack,
			oneway:     isOneway,
		})
	}
	return ways, scanner.Err()
}

// wayLanes returns the lanes in each direction. On a two-way road lanes=* counts
// both directions, so it's split, odd lane forward, unless lanes:forward and
// lanes:backward say otherwise. Every direction gets at least one lane.
func wayLanes(tags pmosm.Tags, oneway bool) (forward, backward int) {
	total, _ := strconv.Atoi(tags.Find("lanes"))
	if oneway {
		return max(total, 1), 0
	}
	forward, backward = (total+1)/2, total/2
	if n, err := strconv.Atoi(tags.Find("lanes:forward")); err == nil && n > 0 {
		forward = n
	}
	if n, err := strconv.Atoi(tags.Find("lanes:backward")); err == nil && n > 0 {
		backward = n
	}
	return max(forward, 1), max(backward, 1)
}

func scanNodes(path string, needed map[pmosm.NodeID]struct{}) (map[pmosm.NodeID]*pmosm.Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := osmpbf.New(context.Background(), f, runtime.NumCPU())
	defer scanner.Close()
	scanner.SkipWays = true
	scanner.SkipRelations = true

	out := make(map[pmosm.NodeID]*pmosm.Node, len(needed))
	for scanner.Scan() {
		n, ok := scanner.Object().(*pmosm.Node)
		if !ok {
			continue
		}
		if _, want := needed[n.ID]; want {
			out[n.ID] = n
		}
	}
	return out, scanner.Err()
}

func assemble(ways []wayRec, nodes map[pmosm.NodeID]*pmosm.Node) (*graph.Graph, error) {
	g := graph.NewGraph()
	idMap := make(map[pmosm.NodeID]int, len(nodes))
	osmIDs := make([]pmosm.NodeID, 0, len(nodes))
	for id := range nodes {
		osmIDs = append(osmIDs, id)
	}
	sort.Slice(osmIDs, func(i, j int) bool { return osmIDs[i] < osmIDs[j] })
	for gid, osmID := range osmIDs {
		n := nodes[osmID]
		idMap[osmID] = gid
		g.AddNode(&graph.Node{ID: gid, Lat: n.Lat, Lon: n.Lon})
	}

	nextEdgeID := 0
	for _, w := range ways {
		for i := 0; i < len(w.nodes)-1; i++ {
			a, aok := nodes[w.nodes[i]]
			b, bok := nodes[w.nodes[i+1]]
			if !aok || !bok {
				continue
			}
			length := haversineMeters(a.Lat, a.Lon, b.Lat, b.Lon)
			if length <= 0 {
				continue
			}
			fromID, toID := idMap[w.nodes[i]], idMap[w.nodes[i+1]]
			g.AddEdge(&graph.Edge{
				ID:         nextEdgeID,
				FromNode:   fromID,
				ToNode:     toID,
				Length:     length,
				Lanes:      w.lanes,
				SpeedLimit: w.speedLimit,
			})
			nextEdgeID++
			if !w.oneway {
				g.AddEdge(&graph.Edge{
					ID:         nextEdgeID,
					FromNode:   toID,
					ToNode:     fromID,
					Length:     length,
					Lanes:      w.lanesBack,
					SpeedLimit: w.speedLimit,
				})
				nextEdgeID++
			}
		}
	}

	if g.NodeCount() == 0 || g.EdgeCount() == 0 {
		return nil, fmt.Errorf("empty graph after assembly: %d nodes, %d edges", g.NodeCount(), g.EdgeCount())
	}
	return g, nil
}
