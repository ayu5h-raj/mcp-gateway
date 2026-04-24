package event

import (
	"sync"
	"time"
)

// subscriberBuffer is the per-subscriber channel size. Slower than the publish
// rate → events are dropped for that subscriber (Publish never blocks).
const subscriberBuffer = 64

// Bus is a fan-out event bus with a bounded ring buffer.
//
// Publish is non-blocking on subscribers (full subscriber channels drop the
// event for that subscriber only). Subscribe returns a channel and an
// unsubscribe function. Recent() returns a snapshot of the ring (oldest →
// newest) for late-attaching subscribers that want history.
type Bus struct {
	mu          sync.Mutex
	ring        []Event
	head        int
	full        bool
	capacity    int
	subscribers []chan Event
}

// New creates a Bus with the given ring capacity (default 1024 if 0 or
// negative).
func New(capacity int) *Bus {
	if capacity <= 0 {
		capacity = 1024
	}
	return &Bus{
		ring:     make([]Event, capacity),
		capacity: capacity,
	}
}

// Publish appends an event to the ring and fans out to subscribers.
// Always non-blocking: full subscriber channels drop the event for that
// subscriber. Time is set if zero.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.mu.Lock()
	b.ring[b.head] = e
	b.head = (b.head + 1) % b.capacity
	if b.head == 0 {
		b.full = true
	}
	subs := append([]chan Event(nil), b.subscribers...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// drop
		}
	}
}

// Subscribe returns a buffered channel that will receive future events, plus
// an unsubscribe function that removes the subscription and closes the channel.
// To replay history, call Recent() before Subscribe (so the snapshot precedes
// any drops).
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subscribers = append(b.subscribers, ch)
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			out := b.subscribers[:0]
			for _, c := range b.subscribers {
				if c != ch {
					out = append(out, c)
				}
			}
			b.subscribers = out
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsub
}

// Recent returns a snapshot of the ring buffer, oldest → newest.
func (b *Bus) Recent() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		out := make([]Event, b.head)
		copy(out, b.ring[:b.head])
		return out
	}
	out := make([]Event, b.capacity)
	copy(out, b.ring[b.head:])
	copy(out[b.capacity-b.head:], b.ring[:b.head])
	return out
}
