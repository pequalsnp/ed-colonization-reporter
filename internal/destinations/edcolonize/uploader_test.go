package edcolonize

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// newTestSession returns a session with just enough state that buildLocked
// produces a snapshot (it returns nil without a commander).
func newTestSession() *state.Session {
	s := state.New()
	s.SetCommander("Kyle", "F123456")
	s.SetSystem("Sol", 10477373803)
	return s
}

func raw(t *testing.T, event string, payload map[string]any) journal.Raw {
	t.Helper()
	payload["event"] = event
	payload["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", event, err)
	}
	r, err := journal.ParseLine(b)
	if err != nil {
		t.Fatalf("parse %s: %v", event, err)
	}
	return r
}

// capture is a test double for the edcolonize receiver.
type capture struct {
	mu       sync.Mutex
	requests []Snapshot
	authHdrs []string
	status   int
	hits     atomic.Int32
}

func (c *capture) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		var snap Snapshot
		_ = json.NewDecoder(r.Body).Decode(&snap)
		c.mu.Lock()
		c.requests = append(c.requests, snap)
		c.authHdrs = append(c.authHdrs, r.Header.Get("Authorization"))
		code := c.status
		c.mu.Unlock()
		if code == 0 {
			code = http.StatusNoContent
		}
		w.WriteHeader(code)
	}))
}

func (c *capture) snapshots() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Snapshot(nil), c.requests...)
}

func TestDisabledDoesNothing(t *testing.T) {
	u := New(SoftwareID{Name: "t", Version: "1"}, Config{Enabled: false}, newTestSession())
	defer u.Close()

	err := u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{}))
	if err != destinations.ErrDisabled {
		t.Errorf("HandleEvent on disabled destination = %v, want ErrDisabled", err)
	}
}

// A URL-less config must also be inert, so a half-filled config file can't
// produce requests to nowhere.
func TestEnabledWithoutURLIsInert(t *testing.T) {
	u := New(SoftwareID{}, Config{Enabled: true, URL: ""}, newTestSession())
	defer u.Close()

	if err := u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{})); err != destinations.ErrDisabled {
		t.Errorf("HandleEvent = %v, want ErrDisabled", err)
	}
}

func TestSnapshotIsPostedWithBearerToken(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	defer srv.Close()

	u := New(SoftwareID{Name: "edcolreport", Version: "9.9"},
		Config{Enabled: true, URL: srv.URL, Token: "shared-secret", MinInterval: time.Millisecond},
		newTestSession())
	defer u.Close()

	if err := u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{})); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	u.Flush()

	snaps := c.snapshots()
	if len(snaps) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snaps))
	}
	if snaps[0].Commander != "Kyle" {
		t.Errorf("commander = %q, want Kyle", snaps[0].Commander)
	}
	if snaps[0].SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", snaps[0].SchemaVersion, SchemaVersion)
	}
	if snaps[0].Location.StarSystem != "Sol" {
		t.Errorf("star_system = %q, want Sol", snaps[0].Location.StarSystem)
	}
	c.mu.Lock()
	auth := c.authHdrs[0]
	c.mu.Unlock()
	if auth != "Bearer shared-secret" {
		t.Errorf("Authorization = %q, want Bearer shared-secret", auth)
	}
}

// The debounce is the whole reason this destination doesn't chatter. A burst
// of events must collapse into a single POST.
func TestDebounceCollapsesBurst(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	defer srv.Close()

	u := New(SoftwareID{}, Config{
		Enabled: true, URL: srv.URL, Token: "t",
		MinInterval: 300 * time.Millisecond,
	}, newTestSession())
	defer u.Close()

	for range 200 {
		if err := u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{})); err != nil {
			t.Fatalf("HandleEvent: %v", err)
		}
	}
	// Well under MinInterval: at most the one leading flush should have run.
	time.Sleep(50 * time.Millisecond)
	if n := c.hits.Load(); n > 1 {
		t.Errorf("200 events produced %d POSTs before the debounce window elapsed, want <= 1", n)
	}

	// After the window, the coalesced snapshot lands.
	time.Sleep(500 * time.Millisecond)
	if n := c.hits.Load(); n == 0 {
		t.Error("no POST after the debounce window elapsed")
	}
	if n := c.hits.Load(); n > 3 {
		t.Errorf("got %d POSTs for a 200-event burst, want a small number", n)
	}
}

// Fail-open: an unreachable or erroring receiver must never surface as a
// per-event error, because that would degrade the reporting Kyle depends on.
func TestUnreachableReceiverFailsOpen(t *testing.T) {
	u := New(SoftwareID{}, Config{
		Enabled: true,
		// Reserved-for-documentation address; connection will fail fast.
		URL:         "http://192.0.2.1:9/api/cmdr/snapshot",
		Token:       "t",
		MinInterval: time.Millisecond,
		Timeout:     100 * time.Millisecond,
	}, newTestSession())
	defer u.Close()

	var errCount atomic.Int32
	u.OnError = func(error) { errCount.Add(1) }

	for range 5 {
		if err := u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{})); err != nil {
			t.Fatalf("HandleEvent returned %v; network failures must not surface per-event", err)
		}
		u.Flush()
	}

	_, failed, failing := u.Stats()
	if failed == 0 || !failing {
		t.Errorf("expected recorded failures, got failed=%d failing=%v", failed, failing)
	}
	// The outage should be reported once, not once per dropped snapshot.
	if n := errCount.Load(); n != 1 {
		t.Errorf("OnError fired %d times during one outage, want 1", n)
	}
}

func TestHTTPErrorStatusCountsAsFailure(t *testing.T) {
	c := &capture{status: http.StatusUnauthorized}
	srv := c.server(t)
	defer srv.Close()

	u := New(SoftwareID{}, Config{
		Enabled: true, URL: srv.URL, Token: "wrong", MinInterval: time.Millisecond,
	}, newTestSession())
	defer u.Close()

	_ = u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{}))
	u.Flush()

	if ok, failed, _ := u.Stats(); ok != 0 || failed != 1 {
		t.Errorf("401 response: ok=%d failed=%d, want ok=0 failed=1", ok, failed)
	}
}

// Recovery should be announced so a silent channel doesn't stay silent after
// the box comes back.
func TestRecoveryIsReported(t *testing.T) {
	c := &capture{status: http.StatusInternalServerError}
	srv := c.server(t)
	defer srv.Close()

	u := New(SoftwareID{}, Config{
		Enabled: true, URL: srv.URL, Token: "t", MinInterval: time.Millisecond,
	}, newTestSession())
	defer u.Close()

	var msgs []string
	var mu sync.Mutex
	u.OnError = func(err error) { mu.Lock(); msgs = append(msgs, err.Error()); mu.Unlock() }

	_ = u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{}))
	u.Flush()

	c.mu.Lock()
	c.status = http.StatusNoContent
	c.mu.Unlock()

	_ = u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{}))
	u.Flush()

	mu.Lock()
	defer mu.Unlock()
	if len(msgs) != 2 {
		t.Fatalf("got %d OnError calls (%v), want 2 (one failure, one recovery)", len(msgs), msgs)
	}
}

func TestNoCommanderYieldsNoPost(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	defer srv.Close()

	// Session with no commander — the state before the first LoadGame.
	u := New(SoftwareID{}, Config{
		Enabled: true, URL: srv.URL, Token: "t", MinInterval: time.Millisecond,
	}, state.New())
	defer u.Close()

	_ = u.HandleEvent(context.Background(), raw(t, "FSDJump", map[string]any{}))
	u.Flush()

	if n := c.hits.Load(); n != 0 {
		t.Errorf("posted %d snapshots with no commander known, want 0", n)
	}
}
