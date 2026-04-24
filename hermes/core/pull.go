package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnknownConsumer is returned by PullEvents when no consumer with the
// given name has been registered.
var ErrUnknownConsumer = errors.New("hermes: unknown pull consumer")

// PullFilter decides whether an inbound message should land in a given
// consumer's buffer. Returning false drops the message for that consumer
// (other consumers and Subscribe handlers still see it).
type PullFilter func(InboundMessage) bool

// PullEvent is one buffered message paired with its monotonic sequence
// number. Consumers persist Seq and pass it back as `since` on the next
// PullEvents call.
type PullEvent struct {
	Seq     uint64        `json:"seq"`
	Message InboundMessage `json:"message"`
}

// pullConsumer is a per-consumer ring buffer with a condition variable so
// PullEvents can long-poll until new events arrive or the context cancels.
type pullConsumer struct {
	name    string
	filter  PullFilter
	cap     int

	mu      sync.Mutex
	cond    *sync.Cond
	events  []PullEvent
	nextSeq uint64
}

func newPullConsumer(name string, capacity int, filter PullFilter) *pullConsumer {
	c := &pullConsumer{
		name:    name,
		filter:  filter,
		cap:     capacity,
		nextSeq: 1,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// push appends msg to the buffer if the filter accepts it. Returns the
// assigned sequence number on accept, 0 on drop.
func (c *pullConsumer) push(msg InboundMessage) uint64 {
	if c.filter != nil && !c.filter(msg) {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := c.nextSeq
	c.nextSeq++
	c.events = append(c.events, PullEvent{Seq: seq, Message: msg})
	if c.cap > 0 && len(c.events) > c.cap {
		c.events = c.events[len(c.events)-c.cap:]
	}
	c.cond.Broadcast()
	return seq
}

// next returns up to max events with Seq > since. Long-polls up to
// timeout for new events when none are immediately available; returns
// nil + nil err on timeout (200 with empty list is the expected idiom).
// Honors ctx cancellation by returning ctx.Err().
func (c *pullConsumer) next(ctx context.Context, since uint64, max int, timeout time.Duration) ([]PullEvent, error) {
	if max <= 0 {
		max = 32
	}
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	// A goroutine bridges ctx + deadline cancellation into the cond var.
	// Without it, cond.Wait() would block forever even after ctx cancels.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var d <-chan time.Time
		if !deadline.IsZero() {
			t := time.NewTimer(time.Until(deadline))
			defer t.Stop()
			d = t.C
		}
		select {
		case <-ctx.Done():
		case <-d:
		case <-stop:
			return
		}
		c.mu.Lock()
		c.cond.Broadcast()
		c.mu.Unlock()
	}()

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		out := c.collectLocked(since, max)
		if len(out) > 0 {
			return out, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return nil, nil
		}
		c.cond.Wait()
	}
}

// collectLocked walks the buffer and returns events with Seq > since,
// up to max. Caller must hold c.mu.
func (c *pullConsumer) collectLocked(since uint64, max int) []PullEvent {
	var out []PullEvent
	for _, ev := range c.events {
		if ev.Seq <= since {
			continue
		}
		out = append(out, ev)
		if len(out) >= max {
			break
		}
	}
	return out
}

// RegisterPullConsumer adds an inbound-message buffer keyed by name with
// a PullFilter selecting which messages it sees and a bounded ring of
// `capacity` events. The same name cannot be registered twice. Phase 1
// posture: in-memory only; Phase 2 Slice I adds replay-on-recovery via
// per-surface inbound history fetch.
func (b *Broker) RegisterPullConsumer(name string, capacity int, filter PullFilter) error {
	if name == "" {
		return errors.New("hermes: pull consumer name required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pullConsumers == nil {
		b.pullConsumers = map[string]*pullConsumer{}
	}
	if _, exists := b.pullConsumers[name]; exists {
		return fmt.Errorf("hermes: pull consumer %q already registered", name)
	}
	b.pullConsumers[name] = newPullConsumer(name, capacity, filter)
	return nil
}

// PullEvents returns up to `max` events for the named consumer with Seq >
// since, long-polling up to timeout when the buffer has none. ctx
// cancellation returns ctx.Err(). Unknown names return ErrUnknownConsumer.
func (b *Broker) PullEvents(ctx context.Context, name string, since uint64, max int, timeout time.Duration) ([]PullEvent, error) {
	b.mu.RLock()
	c, ok := b.pullConsumers[name]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownConsumer, name)
	}
	return c.next(ctx, since, max, timeout)
}

// fanoutPull pushes a delivered InboundMessage to every registered pull
// consumer whose filter accepts it. Called from Broker.deliver alongside
// Subscribe handler fan-out.
func (b *Broker) fanoutPull(msg InboundMessage) {
	b.mu.RLock()
	consumers := make([]*pullConsumer, 0, len(b.pullConsumers))
	for _, c := range b.pullConsumers {
		consumers = append(consumers, c)
	}
	b.mu.RUnlock()
	for _, c := range consumers {
		c.push(msg)
	}
}
