package simulation

import (
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Deathslayer89/MetroSim/internal/events"
)

// outbox is the bus the dispatcher publishes to. It holds events until the
// engine flushes it after releasing e.mu: a subscriber that calls back into the
// engine would otherwise deadlock, and a slow bus would hold up every reader.
// Subscriptions go straight to the underlying bus.
type outbox struct {
	events.Bus
	mu      sync.Mutex
	pending []queued
}

type queued struct {
	topic string
	msg   proto.Message
}

func (o *outbox) Publish(topic string, msg proto.Message) error {
	o.mu.Lock()
	o.pending = append(o.pending, queued{topic, msg})
	o.mu.Unlock()
	return nil
}

// flush publishes everything queued so far, in order.
func (o *outbox) flush() {
	o.mu.Lock()
	batch := o.pending
	o.pending = nil
	o.mu.Unlock()
	for _, q := range batch {
		_ = o.Bus.Publish(q.topic, q.msg)
	}
}
