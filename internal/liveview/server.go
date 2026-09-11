package liveview

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/Deathslayer89/MetroSim/internal/wshub"
)

var droppedFrames = promauto.NewCounter(prometheus.CounterOpts{
	Name: "metrosim_liveview_dropped_frames_total",
	Help: "Websocket frames dropped because a client's send buffer was full.",
})

// Server serves the static frontend and a websocket that pushes a State
// snapshot every TickInterval.
type Server struct {
	State        *State
	TickInterval time.Duration
	StaticDir    string
	hub          *wshub.Hub[struct{}]
}

func NewServer(state *State, staticDir string) *Server {
	return &Server{
		State:        state,
		TickInterval: 100 * time.Millisecond,
		StaticDir:    staticDir,
		hub:          wshub.New[struct{}](droppedFrames),
	}
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("/", http.FileServer(http.Dir(s.StaticDir)))
	mux.HandleFunc("/ws", s.handleWS)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if err := s.hub.Serve(w, r, struct{}{}, nil, nil); err != nil {
		log.Printf("liveview: websocket: %v", err)
	}
}

// Broadcaster pushes a snapshot every TickInterval until ctx is cancelled;
// cancel it on shutdown, since it doesn't watch the HTTP server.
func (s *Server) Broadcaster(ctx context.Context) {
	ticker := time.NewTicker(s.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		payload, err := json.Marshal(s.State.Snapshot())
		if err != nil {
			continue
		}
		s.hub.Broadcast(func(struct{}) []byte { return payload })
	}
}
