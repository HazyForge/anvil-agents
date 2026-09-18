package runapi

import (
	"sync"

	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice-3 standing token hub.
//
// A long-lived WebSocket stream subscription needs live TokenEvents during an
// InProcess turn, not only the post-turn snapshot. The turn path already
// stamps every token with its run name through standingRunSink; this hub
// fans those stamped events out to open stream subscribers for the same
// thread without ever blocking turn execution.
//
// Scoping: subscriptions are keyed by namespace/thread so a thread ID in one
// namespace can never observe another namespace's tokens. The hub is
// process-local (the chart runs one API replica, matching the standing turn
// singleflight guard). SSE stays the snapshot + terminal fallback and never
// subscribes; only WebSocket standing streams attach.
//
// Delivery is non-blocking and bounded: each subscriber drains a buffered
// channel, and a slow reader drops (never stalls the turn). Token frames are
// small JSON documents; the buffer comfortably holds a full Fake-backed turn
// with headroom for a live harness burst.
const standingTokenHubBuffer = 256

// standingTokenHub broadcasts stamped TokenEvents to open stream subscribers.
type standingTokenHub struct {
	mu   sync.Mutex
	subs map[string]map[chan standing.TokenEvent]struct{}
}

func newStandingTokenHub() *standingTokenHub {
	return &standingTokenHub{subs: map[string]map[chan standing.TokenEvent]struct{}{}}
}

func standingTokenHubKey(namespace, threadID string) string {
	return namespace + "/" + threadID
}

// subscribe attaches a buffered receiver for one thread. The caller must call
// the returned unsubscribe function, which also drains the channel so a
// publishing turn never observes a retained subscriber.
func (hub *standingTokenHub) subscribe(namespace, threadID string) (<-chan standing.TokenEvent, func()) {
	if hub == nil {
		events := make(chan standing.TokenEvent)
		close(events)
		return events, func() {}
	}
	events := make(chan standing.TokenEvent, standingTokenHubBuffer)
	key := standingTokenHubKey(namespace, threadID)
	hub.mu.Lock()
	if hub.subs[key] == nil {
		hub.subs[key] = map[chan standing.TokenEvent]struct{}{}
	}
	hub.subs[key][events] = struct{}{}
	hub.mu.Unlock()
	unsubscribed := false
	return events, func() {
		hub.mu.Lock()
		if !unsubscribed {
			unsubscribed = true
			if live := hub.subs[key]; live != nil {
				delete(live, events)
				if len(live) == 0 {
					delete(hub.subs, key)
				}
			}
		}
		hub.mu.Unlock()
	}
}

// publish fans one stamped token event out to the thread's subscribers. It
// never blocks: a full subscriber buffer drops the event so a slow reader
// cannot stall the streaming turn. The durable reply still lands in the turn
// record, so the stream snapshot stays complete even when a live frame drops.
func (hub *standingTokenHub) publish(namespace string, event standing.TokenEvent) {
	if hub == nil {
		return
	}
	hub.mu.Lock()
	var targets []chan standing.TokenEvent
	for events := range hub.subs[standingTokenHubKey(namespace, event.ThreadID)] {
		targets = append(targets, events)
	}
	hub.mu.Unlock()
	for _, events := range targets {
		select {
		case events <- event:
		default:
		}
	}
}

// subscriberCount reports live subscribers for one thread. Tests only.
func (hub *standingTokenHub) subscriberCount(namespace, threadID string) int {
	if hub == nil {
		return 0
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.subs[standingTokenHubKey(namespace, threadID)])
}
