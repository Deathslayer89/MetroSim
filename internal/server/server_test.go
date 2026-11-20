package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Deathslayer89/MetroSim/internal/graph"
	"github.com/Deathslayer89/MetroSim/internal/simulation"
	"github.com/Deathslayer89/MetroSim/internal/traffic"
)

func newTestServer(t *testing.T) (*Server, *simulation.Engine, *websocket.Conn) {
	t.Helper()
	g, err := graph.LoadGraphFromCSV("../../data/graphs/test_nodes.csv", "../../data/graphs/test_edges.csv")
	if err != nil {
		t.Fatal(err)
	}
	engine := simulation.NewEngine(simulation.Config{
		Graph:            g,
		CongestionParams: traffic.DemoCongestionParams(),
		TickRate:         10,
		SpeedMultiplier:  1,
	})
	engine.SpawnVehicle(0)
	engine.SpawnVehicle(1)
	s := NewServer(engine)
	srv := httptest.NewServer(http.HandlerFunc(s.handleWebSocket))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return s, engine, conn
}

func TestWebSocketSendsAnInitialFrameWithTheFleet(t *testing.T) {
	_, _, conn := newTestServer(t)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var u SimulationUpdate
	if err := json.Unmarshal(data, &u); err != nil {
		t.Fatalf("frame isn't a SimulationUpdate: %v", err)
	}
	if u.Type != "initial" || len(u.Vehicles) != 2 || u.Metrics == nil || u.Speed != 1 {
		t.Errorf("initial frame: type %q, %d vehicles, metrics %v, speed %v", u.Type, len(u.Vehicles), u.Metrics, u.Speed)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestControlMessagesReachTheEngine(t *testing.T) {
	_, engine, conn := newTestServer(t)
	go engine.Run()
	t.Cleanup(engine.Stop)
	send := func(msg string) {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the engine to start", func() bool { return engine.GetState() == simulation.StateRunning })

	send(`{"action":"pause"}`)
	waitFor(t, "pause", func() bool { return engine.GetState() == simulation.StatePaused })
	send(`not json`)
	send(`{"action":"speed","value":-3}`)
	send(`{"action":"speed","value":4}`)
	waitFor(t, "speed 4", func() bool { return engine.GetSpeed() == 4 })
	send(`{"action":"resume"}`)
	waitFor(t, "resume", func() bool { return engine.GetState() == simulation.StateRunning })
	send(`{"action":"stop"}`)
	waitFor(t, "stop", func() bool { return engine.GetState() == simulation.StateStopped })
}
