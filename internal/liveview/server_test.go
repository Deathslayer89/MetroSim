package liveview

import (
	"context"
	"testing"
	"time"
)

func TestBroadcasterStopsOnContextCancel(t *testing.T) {
	srv := NewServer(NewState(), "")
	srv.TickInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		srv.Broadcaster(ctx)
		close(done)
	}()

	// Let it tick a few times, then cancel; it must return.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("broadcaster did not exit within 1s of cancel")
	}
}
