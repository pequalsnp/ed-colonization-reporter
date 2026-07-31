package edcolonize

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
)

// This integration is optional. The reporter is a released tool that most
// users will run without ever hearing of edcolonize, so "unconfigured" must
// mean genuinely inert — not merely "doesn't upload". These tests pin that
// contract so a future change can't quietly make the reporter depend on it.

// recorder is a stand-in for the destinations users actually rely on.
type recorder struct{ seen int }

func (r *recorder) Name() string { return "recorder" }
func (r *recorder) HandleEvent(context.Context, journal.Raw) error {
	r.seen++
	return nil
}

// An unconfigured edcolonize destination must not stop events reaching the
// destinations the user actually configured.
func TestUnconfiguredDoesNotBlockOtherDestinations(t *testing.T) {
	before := &recorder{}
	after := &recorder{}
	u := New(SoftwareID{}, Config{}, newTestSession()) // zero config: no URL, not enabled
	defer u.Close()

	var errs []string
	mux := destinations.NewMultiplex(before, u, after)
	mux.OnError = func(name string, err error) { errs = append(errs, name+": "+err.Error()) }

	for range 10 {
		if err := mux.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{})); err != nil {
			t.Fatalf("multiplex HandleEvent: %v", err)
		}
	}

	if before.seen != 10 || after.seen != 10 {
		t.Errorf("destinations saw before=%d after=%d events, want 10 each — an unconfigured "+
			"edcolonize must not interfere with the rest of the pipeline", before.seen, after.seen)
	}
	// ErrDisabled is the conventional sentinel and the multiplex treats it
	// specially; an unconfigured integration must not produce user-visible
	// noise in the activity log.
	if len(errs) != 0 {
		t.Errorf("unconfigured destination surfaced errors: %v", errs)
	}
}

// With no URL configured the integration should not start background work.
// A released desktop app shouldn't grow an idle goroutine for a feature the
// user never asked for.
func TestUnconfiguredStartsNoGoroutines(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()

	u := New(SoftwareID{}, Config{}, newTestSession())
	defer u.Close()

	for range 50 {
		_ = u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{}))
	}
	// A debounce timer, if one were scheduled, would be live by now.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()

	if got := runtime.NumGoroutine(); got > before+1 {
		t.Errorf("goroutines went from %d to %d with no URL configured; "+
			"the integration should be completely inert", before, got)
	}
}

// Nothing should be tracked or retained either — an unconfigured integration
// that still accumulated mission and depot state would be a slow leak in a
// long-running desktop app.
func TestUnconfiguredTracksNothing(t *testing.T) {
	u := New(SoftwareID{}, Config{}, newTestSession())
	defer u.Close()

	_ = u.HandleEvent(context.Background(), raw(t, "MissionAccepted", map[string]any{"MissionID": 1}))
	_ = u.HandleEvent(context.Background(), raw(t, "ColonisationConstructionDepot", map[string]any{"MarketID": 2}))
	_ = u.HandleEvent(context.Background(), raw(t, "Materials", map[string]any{
		"Raw": []map[string]any{{"Name": "iron", "Count": 10}},
	}))

	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.tracked.missions) != 0 || len(u.tracked.depots) != 0 || u.tracked.materials != nil {
		t.Errorf("unconfigured destination accumulated state: missions=%d depots=%d materials=%v",
			len(u.tracked.missions), len(u.tracked.depots), u.tracked.materials != nil)
	}
	if u.dirty {
		t.Error("unconfigured destination marked itself dirty")
	}
}
