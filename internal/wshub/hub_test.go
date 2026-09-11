package wshub

import (
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
