package graph

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
)

// LoadGraphFromCSV reads a nodes file (node_id,lat,lon) and an edges file
// (edge_id,from_node,to_node,length,lanes,speed_limit), each with a header row.
func LoadGraphFromCSV(nodesPath, edgesPath string) (*Graph, error) {
	g := NewGraph()

	if err := loadNodes(g, nodesPath); err != nil {
		return nil, fmt.Errorf("failed to load nodes: %w", err)
	}

	if err := loadEdges(g, edgesPath); err != nil {
		return nil, fmt.Errorf("failed to load edges: %w", err)
	}

	return g, nil
}

func loadNodes(g *Graph, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	if _, err := reader.Read(); err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	lineNum := 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading line %d: %w", lineNum, err)
		}

		if len(record) != 3 {
			return fmt.Errorf("invalid node format at line %d: expected 3 fields, got %d", lineNum, len(record))
		}

		nodeID, err := strconv.Atoi(record[0])
		if err != nil {
			return fmt.Errorf("invalid node ID at line %d: %w", lineNum, err)
		}

		lat, err := strconv.ParseFloat(record[1], 64)
		if err != nil {
			return fmt.Errorf("invalid latitude at line %d: %w", lineNum, err)
		}

		lon, err := strconv.ParseFloat(record[2], 64)
		if err != nil {
			return fmt.Errorf("invalid longitude at line %d: %w", lineNum, err)
		}

		g.AddNode(&Node{
			ID:  nodeID,
			Lat: lat,
			Lon: lon,
		})

		lineNum++
	}

	return nil
}

func loadEdges(g *Graph, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	if _, err := reader.Read(); err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	lineNum := 1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading line %d: %w", lineNum, err)
		}

		if len(record) != 6 {
			return fmt.Errorf("invalid edge format at line %d: expected 6 fields, got %d", lineNum, len(record))
		}

		edgeID, err := strconv.Atoi(record[0])
		if err != nil {
			return fmt.Errorf("invalid edge ID at line %d: %w", lineNum, err)
		}

		fromNode, err := strconv.Atoi(record[1])
		if err != nil {
			return fmt.Errorf("invalid from_node at line %d: %w", lineNum, err)
		}

		toNode, err := strconv.Atoi(record[2])
		if err != nil {
			return fmt.Errorf("invalid to_node at line %d: %w", lineNum, err)
		}

		length, err := strconv.ParseFloat(record[3], 64)
		if err != nil {
			return fmt.Errorf("invalid length at line %d: %w", lineNum, err)
		}

		lanes, err := strconv.Atoi(record[4])
		if err != nil {
			return fmt.Errorf("invalid lanes at line %d: %w", lineNum, err)
		}

		speedLimit, err := strconv.ParseFloat(record[5], 64)
		if err != nil {
			return fmt.Errorf("invalid speed_limit at line %d: %w", lineNum, err)
		}
		if length <= 0 {
			return fmt.Errorf("non-positive length %g at line %d", length, lineNum)
		}
		if speedLimit <= 0 {
			return fmt.Errorf("non-positive speed_limit %g at line %d", speedLimit, lineNum)
		}
		if lanes <= 0 {
			return fmt.Errorf("non-positive lanes %d at line %d (edge would have zero capacity)", lanes, lineNum)
		}

		g.AddEdge(&Edge{
			ID:         edgeID,
			FromNode:   fromNode,
			ToNode:     toNode,
			Length:     length,
			Lanes:      lanes,
			SpeedLimit: speedLimit,
		})

		lineNum++
	}

	return nil
}
