package edcolonize

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The snapshot handed to post() must not alias tracked state. post() marshals
// it on the flush goroutine after u.mu is released, so any pointer still
// shared with trackedState can be written by a concurrent HandleEvent while
// encoding/json is reading it.
//
// This bit for real: the Rank and Progress handlers mutate *Ranks field by
// field rather than replacing the pointer, and buildLocked used to assign
// that live pointer straight into the snapshot. It only reproduced under
// -race on Linux — macOS runs of the same tests stayed green — so these tests
// drive the collision hard enough to be caught anywhere.
//
// Run with -race; without it they pass trivially.
func TestSnapshotDoesNotAliasTrackedState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close the body so the encoder actually runs to completion while the
		// mutating goroutine is still going.
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	u := New(SoftwareID{}, Config{
		Enabled:     true,
		URL:         srv.URL,
		Token:       "t",
		MinInterval: time.Millisecond,
	}, newTestSession())
	defer u.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	// One goroutine keeps rewriting ranks / credits / materials...
	wg.Go(func() {
		for i := range 300 {
			_ = u.HandleEvent(ctx, raw(t, "Rank", map[string]any{
				"Combat": i % 9, "Trade": i % 9, "Explore": i % 9,
				"Empire": i % 14, "Federation": i % 14,
			}))
			_ = u.HandleEvent(ctx, raw(t, "Progress", map[string]any{
				"Combat": i % 100, "Trade": i % 100,
			}))
			_ = u.HandleEvent(ctx, raw(t, "LoadGame", map[string]any{
				"Commander": "Kyle", "Credits": int64(i) * 1000,
			}))
			_ = u.HandleEvent(ctx, raw(t, "Materials", map[string]any{
				"Raw": []map[string]any{{"Name": "iron", "Count": i}},
			}))
		}
	})

	// ...while another keeps forcing snapshots to be built and marshalled.
	wg.Go(func() {
		for range 300 {
			u.Flush()
		}
	})

	wg.Wait()
}

// Depots and missions are rebuilt into fresh slices by buildLocked, but the
// structs they contain carry slices of their own. Exercise that path under
// -race too, so a future "optimisation" that shares a backing array gets
// caught here rather than shipping.
func TestSnapshotDepotsSafeUnderConcurrentUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	u := New(SoftwareID{}, Config{
		Enabled: true, URL: srv.URL, Token: "t", MinInterval: time.Millisecond,
	}, newTestSession())
	defer u.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	wg.Go(func() {
		for i := range 200 {
			_ = u.HandleEvent(ctx, raw(t, "ColonisationConstructionDepot", map[string]any{
				"MarketID":             42,
				"ConstructionProgress": float64(i) / 200,
				"ResourcesRequired": []map[string]any{
					{"Name": "$steel_name;", "RequiredAmount": 500, "ProvidedAmount": i},
				},
			}))
			_ = u.HandleEvent(ctx, raw(t, "MissionAccepted", map[string]any{
				"MissionID": int64(i), "Name": "m",
			}))
		}
	})

	wg.Go(func() {
		for range 200 {
			u.Flush()
		}
	})

	wg.Wait()
}
