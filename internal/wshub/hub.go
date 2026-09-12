// Package wshub fans JSON frames out to websocket clients. Each client gets a
// writer goroutine and holds at most one pending frame: a newer frame replaces
// it, so a slow client skips ahead instead of working through stale ones.
// Writes time out, and a client that stops answering pings is dropped.
package wshub

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
)

// The zero Upgrader rejects cross-origin requests.
var upgrader websocket.Upgrader

// Hub tracks connected clients. T is per-client state fixed at connect time,
// such as a viewport filter.
type Hub[T any] struct {
	dropped   prometheus.Counter
	writeWait time.Duration // limit on each write
	pongWait  time.Duration // a client silent this long, pongs included, is dropped
	mu        sync.RWMutex
	clients   map[*client[T]]struct{}
}

type client[T any] struct {
	conn  *websocket.Conn
	send  chan []byte // holds the one pending frame
	done  chan struct{}
	state T
}

// New returns a hub that counts dropped and replaced frames in dropped.
func New[T any](dropped prometheus.Counter) *Hub[T] {
	return &Hub[T]{
		dropped:   dropped,
		writeWait: 10 * time.Second,
		pongWait:  60 * time.Second,
		clients:   make(map[*client[T]]struct{}),
	}
}

// Serve upgrades the request and blocks until the client disconnects. A non-nil
// first frame goes out before any broadcast, and onMessage, if set, receives
// every message the client sends.
func (h *Hub[T]) Serve(w http.ResponseWriter, r *http.Request, state T, first []byte, onMessage func([]byte)) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	c := &client[T]{conn: conn, send: make(chan []byte, 1), done: make(chan struct{}), state: state}
	h.add(c)
	defer h.remove(c)
	// Written here, before the writer starts, so no broadcast can replace it.
	if first != nil {
		conn.SetWriteDeadline(time.Now().Add(h.writeWait))
		if err := conn.WriteMessage(websocket.TextMessage, first); err != nil {
			conn.Close()
			return nil
		}
	}
	go h.write(c)
	h.read(c, onMessage)
	return nil
}

// Broadcast queues payload(state) for every client, skipping nil payloads. A
// frame the client hasn't taken yet is replaced by the new one.
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
			continue
		default:
		}
		select {
		case <-c.send:
			h.dropped.Inc()
		default:
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

// read returns once the client disconnects or goes quiet for pongWait; a
// browser answers every ping, which pushes the deadline back.
func (h *Hub[T]) read(c *client[T], onMessage func([]byte)) {
	defer c.conn.Close()
	c.conn.SetReadLimit(8192)
	c.conn.SetReadDeadline(time.Now().Add(h.pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(h.pongWait))
	})
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

// write sends frames and pings until the client leaves or a write times out;
// closing the connection then ends read too.
func (h *Hub[T]) write(c *client[T]) {
	ping := time.NewTicker(h.pongWait * 9 / 10)
	defer ping.Stop()
	defer c.conn.Close()
	for {
		select {
		case <-c.done:
			return
		case p := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(h.writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, p); err != nil {
				return
			}
		case <-ping.C:
			c.conn.SetWriteDeadline(time.Now().Add(h.writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
