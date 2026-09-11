// Package wshub fans JSON frames out to websocket clients. Each client gets a
// writer goroutine and a bounded send buffer; when the buffer is full the
// frame is dropped rather than stalling the broadcast.
package wshub

import (
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
)

const sendBuffer = 64

// The zero Upgrader rejects cross-origin requests.
var upgrader websocket.Upgrader

// Hub tracks connected clients. T is per-client state fixed at connect time,
// such as a viewport filter.
type Hub[T any] struct {
	dropped prometheus.Counter
	mu      sync.RWMutex
	clients map[*client[T]]struct{}
}

type client[T any] struct {
	conn  *websocket.Conn
	send  chan []byte
	done  chan struct{}
	state T
}

// New returns a hub that counts dropped frames in dropped.
func New[T any](dropped prometheus.Counter) *Hub[T] {
	return &Hub[T]{dropped: dropped, clients: make(map[*client[T]]struct{})}
}

// Serve upgrades the request and blocks until the client disconnects. A non-nil
// first frame goes out before any broadcast, and onMessage, if set, receives
// every message the client sends.
func (h *Hub[T]) Serve(w http.ResponseWriter, r *http.Request, state T, first []byte, onMessage func([]byte)) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	c := &client[T]{conn: conn, send: make(chan []byte, sendBuffer), done: make(chan struct{}), state: state}
	if first != nil {
		c.send <- first
	}
	h.add(c)
	go c.write()
	c.read(onMessage)
	h.remove(c)
	return nil
}

// Broadcast queues payload(state) for every client, skipping nil payloads.
func (h *Hub[T]) Broadcast(payload func(state T) []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		p := payload(c.state)
		if p == nil {
			continue
		}
		select {
		case c.send <- p:
		default:
			h.dropped.Inc()
		}
	}
}

func (h *Hub[T]) add(c *client[T]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub[T]) remove(c *client[T]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.done)
	}
}

func (c *client[T]) read(onMessage func([]byte)) {
	defer c.conn.Close()
	c.conn.SetReadLimit(8192)
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if onMessage != nil {
			onMessage(msg)
		}
	}
}

func (c *client[T]) write() {
	defer c.conn.Close()
	for {
		select {
		case <-c.done:
			return
		case p := <-c.send:
			if err := c.conn.WriteMessage(websocket.TextMessage, p); err != nil {
				return
			}
		}
	}
}
