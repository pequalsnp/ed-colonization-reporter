package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/edcolonize"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
)

func sampleEvent(t *testing.T) journal.Raw {
	t.Helper()
	r, err := journal.ParseLine([]byte(`{"timestamp":"2026-07-31T00:00:00Z","event":"FSDJump"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

// Multiplex.Replace swaps the WHOLE destination set, so any destination
// omitted from the call is silently unhooked until the app restarts. That is
// exactly what happened to edcolonize: it was added to NewMultiplex but left
// out of both Replace call sites, so the first Settings save killed the push
// channel with no error anywhere.
//
// This pins every destination surviving a config apply. If you add a new one,
// this test fails until it's threaded through ApplyConfig.
func TestApplyConfigKeepsAllDestinationsRegistered(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)
	// buildLocked returns nil without a commander, so an unattributed session
	// would make this test pass for the wrong reason.
	s.session.SetCommander("Jameson", "F1")

	var hits atomic.Int32
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer capture.Close()

	s.edcolonize = edcolonize.New(
		edcolonize.SoftwareID{Name: "test", Version: "1"},
		edcolonize.Config{Enabled: true, URL: capture.URL, Token: "tok", MinInterval: time.Millisecond},
		s.session,
	)
	t.Cleanup(s.edcolonize.Close)
	s.mux = destinations.NewMultiplex(s.rep, s.edcolonize)

	// A Settings save that leaves edcolonize enabled must not unhook it.
	cfg := s.Config()
	cfg.EdcolonizeEnabled = true
	cfg.EdcolonizeURL = capture.URL
	cfg.EdcolonizeToken = "tok"
	if err := s.ApplyConfig(cfg); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	if err := s.mux.HandleEvent(context.Background(), sampleEvent(t)); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	// The debounce flush runs on a timer goroutine, so poll rather than
	// assuming it has already landed.
	deadline := time.Now().Add(3 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		s.edcolonize.Flush()
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Error("edcolonize received no events after a config apply — it was dropped " +
			"from the multiplex; Replace must list every destination")
	}
}

// The browser Settings page and the Fyne panel must agree on the same
// fields, and both route through ApplyConfig. This covers the HTTP surface:
// a POST must persist the edcolonize settings and hand them back on GET.
func TestConfigAPIRoundTripsEdcolonizeSettings(t *testing.T) {
	s, ts := newTestServer(t)
	s.edcolonize = edcolonize.New(
		edcolonize.SoftwareID{}, edcolonize.Config{}, s.session,
	)
	t.Cleanup(s.edcolonize.Close)

	body, _ := json.Marshal(configDTO{
		EdcolonizeEnabled: true,
		EdcolonizeURL:     "http://box.local:3000/api/cmdr/snapshot",
		EdcolonizeToken:   "shared-secret",
	})
	r, err := http.Post(ts.URL+"/api/config", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /api/config = %d, want 204", r.StatusCode)
	}

	// In-memory config updated...
	if !s.Config().EdcolonizeEnabled || s.Config().EdcolonizeURL == "" {
		t.Errorf("config not applied: %+v", s.Config())
	}

	// ...and served back, so the Settings form repopulates.
	r2, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Body.Close() }()
	var got configDTO
	if err := json.NewDecoder(r2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.EdcolonizeEnabled {
		t.Error("edcolonize_enabled did not round-trip")
	}
	if got.EdcolonizeURL != "http://box.local:3000/api/cmdr/snapshot" {
		t.Errorf("edcolonize_url = %q", got.EdcolonizeURL)
	}
	if got.EdcolonizeToken != "shared-secret" {
		t.Errorf("edcolonize_token did not round-trip")
	}
}

// Turning the destination off through Settings must actually stop it, not
// just stop it from being rebuilt on next launch.
func TestApplyConfigCanDisableEdcolonize(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)

	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer capture.Close()

	s.edcolonize = edcolonize.New(edcolonize.SoftwareID{}, edcolonize.Config{
		Enabled: true, URL: capture.URL, Token: "t",
	}, s.session)
	t.Cleanup(s.edcolonize.Close)
	s.mux = destinations.NewMultiplex(s.rep, s.edcolonize)

	cfg := s.Config()
	cfg.EdcolonizeEnabled = false
	if err := s.ApplyConfig(cfg); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	before, beforeFail, _ := s.edcolonize.Stats()
	if err := s.mux.HandleEvent(context.Background(), sampleEvent(t)); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	s.edcolonize.Flush()
	after, afterFail, _ := s.edcolonize.Stats()

	if after != before || afterFail != beforeFail {
		t.Errorf("disabled destination still pushed: ok %d->%d failed %d->%d",
			before, after, beforeFail, afterFail)
	}
}
