package edsm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// discardEndpoint returns the journal events EDSM tells clients to drop.
// The list is curated server-side so that EDMC-style clients don't flood
// the API with noise that EDSM doesn't store.
const discardEndpoint = "https://www.edsm.net/api-journal-v1/discard"

// discardSet tracks the events EDSM wants us to skip. Safe for concurrent use.
type discardSet struct {
	mu     sync.RWMutex
	events map[string]bool
	fetched bool
}

func newDiscardSet() *discardSet {
	return &discardSet{events: map[string]bool{}}
}

// Has reports whether name is in the discard list. Returns false if the
// list has never been fetched (fail-open: we'd rather upload too much than
// silently drop events).
func (d *discardSet) Has(name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.events[name]
}

// Fetched reports whether the list has ever been successfully populated.
func (d *discardSet) Fetched() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.fetched
}

// Refresh fetches the latest discard list. EDMC overrides the EDSM-supplied
// list by always re-enabling "Docked" — we follow that convention.
func (d *discardSet) Refresh(ctx context.Context, hc *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discardEndpoint, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("EDSM discard list: %s — %s", resp.Status, body)
	}
	var names []string
	if err := json.NewDecoder(resp.Body).Decode(&names); err != nil {
		return fmt.Errorf("decode discard list: %w", err)
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		if n == "Docked" {
			// EDMC unconditionally re-enables Docked; the rest of the
			// uploader logic depends on this event arriving.
			continue
		}
		set[n] = true
	}
	d.mu.Lock()
	d.events = set
	d.fetched = true
	d.mu.Unlock()
	return nil
}

// RefreshLoop periodically refreshes the discard list. The first refresh
// is attempted immediately; subsequent refreshes happen every `interval`.
// Returns when ctx is cancelled.
func (d *discardSet) RefreshLoop(ctx context.Context, hc *http.Client, interval time.Duration, onErr func(error)) {
	doOnce := func() {
		if err := d.Refresh(ctx, hc); err != nil && onErr != nil {
			onErr(err)
		}
	}
	doOnce()
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			doOnce()
		}
	}
}
