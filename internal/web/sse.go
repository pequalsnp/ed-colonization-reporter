package web

import (
	"sync"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

// statusHub fans status events out to subscribed SSE clients. It is the
// reporter's status sink; one instance lives on the Server.
type statusHub struct {
	mu          sync.Mutex
	subscribers map[chan reporter.Status]struct{}
	buffer      []reporter.Status // last N events, replayed to new subscribers
}

func newStatusHub() *statusHub {
	return &statusHub{subscribers: map[chan reporter.Status]struct{}{}}
}

// Publish broadcasts s to every current subscriber. Subscribers that aren't
// keeping up drop the event rather than blocking the publisher.
func (h *statusHub) Publish(s reporter.Status) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buffer = append(h.buffer, s)
	const maxBuffered = 200
	if len(h.buffer) > maxBuffered {
		h.buffer = h.buffer[len(h.buffer)-maxBuffered:]
	}
	for ch := range h.subscribers {
		select {
		case ch <- s:
		default:
			// subscriber is too slow; drop this event for them.
		}
	}
}

// Subscribe registers a new client and replays the buffered backlog so the
// activity log isn't blank on page load.
func (h *statusHub) Subscribe() (<-chan reporter.Status, func()) {
	ch := make(chan reporter.Status, 64)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	backlog := append([]reporter.Status(nil), h.buffer...)
	h.mu.Unlock()
	go func() {
		for _, s := range backlog {
			select {
			case ch <- s:
			default:
				return
			}
		}
	}()
	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}
