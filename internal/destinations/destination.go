// Package destinations defines the common interface every "place we send
// journal events to" must implement, plus the multiplexer that fans events
// out across them.
//
// Each destination — ravencolonial, EDDN, EDSM, Inara — lives in its own
// subpackage and decides for itself which events it cares about. The
// multiplexer doesn't know or care about per-destination semantics; it just
// calls HandleEvent on every destination for every event.
package destinations

import (
	"context"
	"errors"
	"sync"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

// Destination is anything that consumes parsed journal events. Implementations
// must be safe for concurrent calls and must not block longer than necessary
// — slow API calls should be issued by HandleEvent without locking up the
// caller for the response (the multiplexer dispatches serially per event).
type Destination interface {
	// Name returns a short human-readable label used in logs and UI.
	Name() string
	// HandleEvent processes one parsed journal line. Returning an error does
	// not abort other destinations or future events; the multiplexer logs it
	// and continues.
	HandleEvent(ctx context.Context, raw journal.Raw) error
}

// Multiplex fans events out to N destinations. A nil or empty Multiplex is
// a no-op handler.
type Multiplex struct {
	mu           sync.RWMutex
	destinations []Destination
	// OnError, if set, receives every per-destination error so callers can
	// surface them in the UI activity log.
	OnError func(name string, err error)
}

// NewMultiplex builds a Multiplex with an initial set of destinations.
func NewMultiplex(dests ...Destination) *Multiplex {
	return &Multiplex{destinations: append([]Destination(nil), dests...)}
}

// Add registers an additional destination. Safe to call while events are
// flowing; the new destination starts receiving events on the next one.
func (m *Multiplex) Add(d Destination) {
	if d == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destinations = append(m.destinations, d)
}

// Replace swaps the destination set atomically. Useful when the user toggles
// destinations in Settings and we need to rebuild from new config.
func (m *Multiplex) Replace(dests ...Destination) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destinations = append([]Destination(nil), dests...)
}

// HandleEvent dispatches raw to every registered destination, in registration
// order. Errors are surfaced via OnError if set, but never abort the loop.
// The returned error is always nil today; reserved for future use (e.g. an
// aggregate error type once we have more sophisticated retry semantics).
func (m *Multiplex) HandleEvent(ctx context.Context, raw journal.Raw) error {
	m.mu.RLock()
	dests := m.destinations
	m.mu.RUnlock()
	for _, d := range dests {
		if err := d.HandleEvent(ctx, raw); err != nil {
			if m.OnError != nil {
				m.OnError(d.Name(), err)
			}
		}
	}
	return nil
}

// ErrDisabled is the conventional sentinel a destination's HandleEvent may
// return when its enable flag is off. The multiplexer treats it specially:
// it does not invoke OnError for ErrDisabled, since the user explicitly
// asked for silence.
var ErrDisabled = errors.New("destination disabled")

// Compile-time check that the existing reporter.Reporter is a valid
// Destination. The reporter's Name() and HandleEvent are defined alongside
// the reporter itself; this guard catches drift.
var _ Destination = (*reporter.Reporter)(nil)
