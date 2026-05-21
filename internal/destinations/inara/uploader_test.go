package inara

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

type serverCapture struct {
	mu       sync.Mutex
	requests []Request
	respCode int
	respBody string
}

func (c *serverCapture) record(r Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, r)
}

func (c *serverCapture) snapshot() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Request, len(c.requests))
	copy(out, c.requests)
	return out
}

func newCaptureServer(t *testing.T, c *serverCapture) *httptest.Server {
	t.Helper()
	if c.respCode == 0 {
		c.respCode = 200
	}
	if c.respBody == "" {
		c.respBody = `{"header":{"eventStatus":200,"eventStatusText":"OK"},"events":[{"eventStatus":200,"eventStatusText":"OK"}]}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("server got bad JSON: %v; body=%s", err, body)
			http.Error(w, "bad json", 400)
			return
		}
		c.record(req)
		w.WriteHeader(c.respCode)
		_, _ = w.Write([]byte(c.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupEnabled(t *testing.T, sess *state.Session, c *serverCapture) *Uploader {
	t.Helper()
	srv := newCaptureServer(t, c)
	u := New(SoftwareID{Name: "edcolreport-test", Version: "0.0.1"}, sess)
	u.Endpoint = srv.URL
	u.SetAPIKey("test-key")
	u.SetEnabled(true)
	return u
}

func TestUploader_DisabledReturnsErrDisabled(t *testing.T) {
	sess := state.New()
	u := New(SoftwareID{Name: "t"}, sess)
	r := raw(t, journal.EventFSDJump, map[string]any{"StarSystem": "Sol", "StarPos": []any{0, 0, 0}})
	if err := u.HandleEvent(context.Background(), r); !errors.Is(err, destinations.ErrDisabled) {
		t.Errorf("got %v, want ErrDisabled", err)
	}
}

func TestUploader_EnqueuesThenFlushes(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 100, "StarPos": []any{0.0, 0.0, 0.0},
	})
	if err := u.HandleEvent(context.Background(), r); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if u.QueueLen() != 2 {
		t.Errorf("queue len after FSDJump = %d, want 2 (location + jump)", u.QueueLen())
	}
	if err := u.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	reqs := c.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Header.APIKey != "test-key" || req.Header.CommanderName != "Jameson" {
		t.Errorf("header wrong: %+v", req.Header)
	}
	if req.Header.CommanderFrontierID != "F1" {
		t.Errorf("FID = %q, want F1", req.Header.CommanderFrontierID)
	}
	if len(req.Events) != 2 {
		t.Errorf("events = %d, want 2", len(req.Events))
	}
	if u.QueueLen() != 0 {
		t.Errorf("queue should be empty after Flush; got %d", u.QueueLen())
	}
}

func TestUploader_DoesNothingWithoutAPIKey(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	srv := newCaptureServer(t, c)
	u := New(SoftwareID{Name: "t", Version: "x"}, sess)
	u.Endpoint = srv.URL
	u.SetEnabled(true) // no key

	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	})
	if err := u.HandleEvent(context.Background(), r); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if err := u.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := len(c.snapshot()); got != 0 {
		t.Errorf("should not have flushed without API key; saw %d requests", got)
	}
}

func TestUploader_SkipsBetaGalaxy(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.0.0.beta1", "x")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	})
	if err := u.HandleEvent(context.Background(), r); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if u.QueueLen() != 0 {
		t.Errorf("beta should not enqueue; got %d", u.QueueLen())
	}
}

func TestUploader_BatchAccumulatesAcrossEvents(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	// FSDJump (-> 2 events) then CarrierJump (-> 2 events) then Docked (suppressed).
	r1 := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	})
	r2 := raw(t, journal.EventCarrierJump, map[string]any{
		"StarSystem": "Sol", "StationName": "MY-FC", "MarketID": 1, "StarPos": []any{0, 0, 0},
	})
	r3 := raw(t, journal.EventDocked, map[string]any{
		"StarSystem": "Sol", "StationName": "MY-FC", "MarketID": 1,
	})
	for _, r := range []journal.Raw{r1, r2, r3} {
		if err := u.HandleEvent(context.Background(), r); err != nil {
			t.Fatalf("HandleEvent: %v", err)
		}
	}
	if u.QueueLen() != 4 {
		t.Errorf("queue = %d, want 4 (FSDJump:2 + CarrierJump:2 + suppressed Docked:0)", u.QueueLen())
	}
	if err := u.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.snapshot()
	if len(reqs) != 1 || len(reqs[0].Events) != 4 {
		t.Errorf("batched call wrong: %d requests with first having %d events", len(reqs), func() int {
			if len(reqs) == 0 {
				return 0
			}
			return len(reqs[0].Events)
		}())
	}
}

func TestUploader_5xxRequeues(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{respCode: 503, respBody: "Service Unavailable"}
	u := setupEnabled(t, sess, c)

	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	})
	if err := u.HandleEvent(context.Background(), r); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if u.QueueLen() != 2 {
		t.Fatalf("pre-flush queue = %d", u.QueueLen())
	}
	if err := u.Flush(context.Background()); err == nil {
		t.Fatal("expected error on 503")
	}
	if u.QueueLen() != 2 {
		t.Errorf("5xx should requeue; got queue len %d, want 2", u.QueueLen())
	}
}

func TestUploader_4xxDropsBatch(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{respCode: 400, respBody: "Bad Request"}
	u := setupEnabled(t, sess, c)

	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	})
	_ = u.HandleEvent(context.Background(), r)
	if err := u.Flush(context.Background()); err == nil {
		t.Fatal("expected error on 400")
	}
	if u.QueueLen() != 0 {
		t.Errorf("4xx must drop batch (not retry); queue len = %d", u.QueueLen())
	}
}

func TestUploader_BackgroundFlush(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u.StartBackground(ctx, 50*time.Millisecond)

	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	})
	if err := u.HandleEvent(context.Background(), r); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	deadline := time.After(2 * time.Second)
	var reqs []Request
	for {
		reqs = c.snapshot()
		if len(reqs) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("background flush never happened; queue=%d", u.QueueLen())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if len(reqs[0].Events) != 2 {
		t.Errorf("first request events = %d", len(reqs[0].Events))
	}
}

func TestUploader_EmptyFlushIsNoop(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)
	if err := u.Flush(context.Background()); err != nil {
		t.Fatalf("Flush on empty queue: %v", err)
	}
	if len(c.snapshot()) != 0 {
		t.Error("empty Flush should not hit the server")
	}
}

func TestUploader_HeaderIncludesFID(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1234567")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)
	_ = u.HandleEvent(context.Background(), raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	}))
	if err := u.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.snapshot()
	if len(reqs) != 1 || reqs[0].Header.CommanderFrontierID != "F1234567" {
		t.Errorf("FID missing/wrong in header: %+v", reqs)
	}
}

func TestUploader_DisabledFlushIsNoop(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	// Enqueue some work, then disable, then flush.
	_ = u.HandleEvent(context.Background(), raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	}))
	u.SetEnabled(false)
	if err := u.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.snapshot()) != 0 {
		t.Errorf("disabled Flush should be no-op")
	}
	// Queue should be preserved so a subsequent re-enable + flush works.
	if u.QueueLen() != 2 {
		t.Errorf("queue should be preserved while disabled; got %d", u.QueueLen())
	}
}

func TestUploader_HeaderAuthFailDoesNotRetry(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{
		respCode: 200,
		respBody: `{"header":{"eventStatus":400,"eventStatusText":"Invalid API key"},"events":[]}`,
	}
	u := setupEnabled(t, sess, c)
	_ = u.HandleEvent(context.Background(), raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	}))
	if err := u.Flush(context.Background()); err == nil {
		t.Fatal("expected error on header.eventStatus=400")
	}
	if u.QueueLen() != 0 {
		t.Errorf("auth failure must drop batch; queue = %d", u.QueueLen())
	}
}
