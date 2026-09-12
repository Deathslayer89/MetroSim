package wshub

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
)

func dial(t *testing.T, srv *httptest.Server, query string, header http.Header) (*websocket.Conn, error) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?"+query, header)
	return conn, err
}

func readText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(msg)
}

func newTestServer(hub *Hub[string], got chan<- string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = hub.Serve(w, r, r.URL.Query().Get("tag"), []byte("hello"), func(msg []byte) { got <- string(msg) })
	}))
}

func TestHubFirstFrameBroadcastAndMessages(t *testing.T) {
	hub := New[string](prometheus.NewCounter(prometheus.CounterOpts{Name: "dropped_total", Help: "test"}))
	got := make(chan string, 1)
	srv := newTestServer(hub, got)
	defer srv.Close()

	conn, err := dial(t, srv, "tag=a", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if msg := readText(t, conn); msg != "hello" {
		t.Fatalf("first frame: want hello, got %q", msg)
	}
	hub.Broadcast(func(tag string) []byte { return []byte("frame for " + tag) })
	if msg := readText(t, conn); msg != "frame for a" {
		t.Fatalf("broadcast: want %q, got %q", "frame for a", msg)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("pause")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case msg := <-got:
		if msg != "pause" {
			t.Errorf("onMessage: want pause, got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onMessage never saw the client's message")
	}
}

func TestHubRejectsCrossOrigin(t *testing.T) {
	hub := New[string](prometheus.NewCounter(prometheus.CounterOpts{Name: "dropped_total", Help: "test"}))
	srv := newTestServer(hub, make(chan string, 1))
	defer srv.Close()

	header := http.Header{"Origin": []string{"https://evil.example"}}
	if conn, err := dial(t, srv, "", header); err == nil {
		conn.Close()
		t.Fatal("a cross-origin upgrade should be refused")
	}
}

func clientCount[T any](h *Hub[T]) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// A client that stalls gets the newest frame once it reads again, not a backlog
// of stale ones ending before it.
func TestSlowClientGetsTheLatestFrame(t *testing.T) {
	hub := New[string](prometheus.NewCounter(prometheus.CounterOpts{Name: "dropped_total", Help: "test"}))
	srv := newTestServer(hub, make(chan string, 1))
	defer srv.Close()
	conn, err := dial(t, srv, "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if msg := readText(t, conn); msg != "hello" {
		t.Fatalf("first frame: want hello, got %q", msg)
	}

	for i := 1; i <= 500; i++ {
		hub.Broadcast(func(string) []byte { return []byte(fmt.Sprintf("frame-%04d", i)) })
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("never got the newest frame: %v", err)
		}
		if string(msg) == "frame-0500" {
			return
		}
	}
}

// A client that stops answering pings is dropped once its read deadline passes,
// instead of keeping its goroutines alive.
func TestUnresponsiveClientIsDropped(t *testing.T) {
	hub := New[string](prometheus.NewCounter(prometheus.CounterOpts{Name: "dropped_total", Help: "test"}))
	hub.writeWait, hub.pongWait = 100*time.Millisecond, 300*time.Millisecond
	srv := newTestServer(hub, make(chan string, 1))
	defer srv.Close()
	conn, err := dial(t, srv, "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close() // never read, so no pongs go back

	deadline := time.Now().Add(3 * time.Second)
	for clientCount(hub) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	for clientCount(hub) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if clientCount(hub) != 0 {
		t.Fatal("a client that never answered a ping is still registered")
	}
}
