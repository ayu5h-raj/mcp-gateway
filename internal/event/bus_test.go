package event

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_PublishThenRecent(t *testing.T) {
	b := New(8)
	b.Publish(Event{Kind: "x", Server: "a"})
	b.Publish(Event{Kind: "x", Server: "b"})

	got := b.Recent()
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Server)
	assert.Equal(t, "b", got[1].Server)
}

func TestBus_RingOverwritesAtCapacity(t *testing.T) {
	b := New(3)
	for i := 0; i < 10; i++ {
		b.Publish(Event{Kind: "x", Method: string(rune('a' + i))})
	}
	got := b.Recent()
	require.Len(t, got, 3)
	// Last three: "h", "i", "j"
	assert.Equal(t, "h", got[0].Method)
	assert.Equal(t, "i", got[1].Method)
	assert.Equal(t, "j", got[2].Method)
}

func TestBus_SubscribeReceivesPublished(t *testing.T) {
	b := New(8)
	ch, unsub := b.Subscribe()
	defer unsub()

	b.Publish(Event{Kind: "x", Server: "a"})

	select {
	case e := <-ch:
		assert.Equal(t, "a", e.Server)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive published event")
	}
}

func TestBus_UnsubscribeStopsDelivery(t *testing.T) {
	b := New(8)
	ch, unsub := b.Subscribe()
	unsub()

	b.Publish(Event{Kind: "x"})
	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("subscriber received event after unsub: %v", e)
		}
	case <-time.After(100 * time.Millisecond):
		// Pass: no delivery.
	}
}

func TestBus_SlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := New(8)
	ch, unsub := b.Subscribe()
	defer unsub()

	// Don't drain ch. Publish 1000 events; bus must not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(Event{Kind: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}

	// Some events were dropped; ch is bounded. Drain what's there to confirm
	// no panic.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			require.Greater(t, drained, 0)
			require.Less(t, drained, 1000) // dropped some
			return
		}
	}
}

func TestBus_ConcurrentPublishersAreSafe(t *testing.T) {
	b := New(1024)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Publish(Event{Kind: "x"})
			}
		}()
	}
	wg.Wait()
}
