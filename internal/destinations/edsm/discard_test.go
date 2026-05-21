package edsm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscardSet_RefreshFromMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`["Music","Docked","ReceiveText","FileHeader"]`))
	}))
	defer srv.Close()

	d := newDiscardSet()
	if err := d.RefreshFrom(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL); err != nil {
		t.Fatalf("RefreshFrom: %v", err)
	}
	if !d.Fetched() {
		t.Error("Fetched should be true after a successful refresh")
	}
	if !d.Has("Music") || !d.Has("ReceiveText") || !d.Has("FileHeader") {
		t.Error("discarded events should be in the set")
	}
	if d.Has("Docked") {
		t.Error("Docked must be re-enabled even when the server discards it")
	}
}

func TestDiscardSet_RefreshNon200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	d := newDiscardSet()
	if err := d.RefreshFrom(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL); err == nil {
		t.Error("expected error on 503")
	}
	if d.Fetched() {
		t.Error("failed refresh must not flip Fetched")
	}
}

func TestDiscardSet_RefreshBadJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	d := newDiscardSet()
	if err := d.RefreshFrom(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL); err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestDiscardSet_FetchedFalseUntilSuccess(t *testing.T) {
	d := newDiscardSet()
	if d.Fetched() {
		t.Error("fresh discard set should not report Fetched")
	}
	// Has() must fail open on an unfetched set so we don't silently drop
	// every event on startup before the discard list is loaded.
	if d.Has("Docked") {
		t.Error("unfetched discard set should fail-open (Has returns false)")
	}
}

func TestDiscardSet_HasUnknownEvent(t *testing.T) {
	d := newDiscardSet()
	d.mu.Lock()
	d.events = map[string]bool{"X": true}
	d.fetched = true
	d.mu.Unlock()
	if d.Has("Y") {
		t.Error("Has on unknown event should be false")
	}
}
