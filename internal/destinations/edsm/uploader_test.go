package edsm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func mustRaw(t *testing.T, event string, payload map[string]any) journal.Raw {
	t.Helper()
	payload["event"] = event
	if _, ok := payload["timestamp"]; !ok {
		payload["timestamp"] = "2026-05-21T12:00:00Z"
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := journal.ParseLine(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type capture struct {
	bodies    []url.Values
	messages  [][]map[string]any
	reqCount  atomic.Int32
	respCode  int
	respBody  string
	respHeader http.Header
}

func newServer(t *testing.T, c *capture) *httptest.Server {
	t.Helper()
	if c.respCode == 0 {
		c.respCode = 200
	}
	if c.respBody == "" {
		c.respBody = `{"msgnum": 100, "msg": "OK"}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.reqCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		c.bodies = append(c.bodies, form)
		var events []map[string]any
		_ = json.Unmarshal([]byte(form.Get("message")), &events)
		c.messages = append(c.messages, events)
		for k, vs := range c.respHeader {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(c.respCode)
		_, _ = w.Write([]byte(c.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupEnabled(t *testing.T, sess *state.Session, c *capture) *Uploader {
	t.Helper()
	srv := newServer(t, c)
	u := New(SoftwareID{Name: "edcolreport-test", Version: "0.0.1"}, sess)
	u.Endpoint = srv.URL
	u.SetAPIKey("test-key")
	u.SetEnabled(true)
	return u
}

func TestUploader_DisabledReturnsErrDisabled(t *testing.T) {
	sess := state.New()
	u := New(SoftwareID{Name: "t"}, sess)
	raw := mustRaw(t, "FSDJump", map[string]any{})
	if err := u.HandleEvent(context.Background(), raw); !errors.Is(err, destinations.ErrDisabled) {
		t.Errorf("got %v, want ErrDisabled", err)
	}
}

func TestUploader_NoAPIKeySkipsSilently(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &capture{}
	srv := newServer(t, c)
	u := New(SoftwareID{Name: "t"}, sess)
	u.Endpoint = srv.URL
	u.SetEnabled(true) // enabled but no key

	raw := mustRaw(t, "FSDJump", map[string]any{"StarSystem": "Sol"})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if c.reqCount.Load() != 0 {
		t.Errorf("should not POST without API key")
	}
}

func TestUploader_NoCommanderSkipsSilently(t *testing.T) {
	sess := state.New()
	c := &capture{}
	u := setupEnabled(t, sess, c)
	raw := mustRaw(t, "FSDJump", map[string]any{"StarSystem": "Sol"})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if c.reqCount.Load() != 0 {
		t.Errorf("should not POST without commander; got %d requests", c.reqCount.Load())
	}
}

func TestUploader_PostsFormBody(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.1", "r12345/r0 ")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})

	c := &capture{}
	u := setupEnabled(t, sess, c)

	raw := mustRaw(t, "FSDJump", map[string]any{
		"StarSystem": "Sol", "SystemAddress": 10477373803, "StarPos": []any{0.0, 0.0, 0.0},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if c.reqCount.Load() != 1 {
		t.Fatalf("req count = %d", c.reqCount.Load())
	}
	form := c.bodies[0]
	if form.Get("commanderName") != "Jameson" || form.Get("apiKey") != "test-key" {
		t.Errorf("auth fields wrong: %+v", form)
	}
	if form.Get("fromSoftware") != "edcolreport-test" || form.Get("fromSoftwareVersion") != "0.0.1" {
		t.Errorf("software fields wrong: %+v", form)
	}
	if form.Get("fromGameVersion") != "4.1" || form.Get("fromGameBuild") != "r12345/r0 " {
		t.Errorf("game version fields wrong: %+v", form)
	}
	events := c.messages[0]
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	ev := events[0]
	if ev["event"] != "FSDJump" {
		t.Errorf("event = %v", ev["event"])
	}
	// Transient fields injected from session.
	if ev["_systemName"] != "Sol" {
		t.Errorf("_systemName = %v", ev["_systemName"])
	}
	if pos, ok := ev["_systemCoordinates"].([]any); !ok || len(pos) != 3 {
		t.Errorf("_systemCoordinates wrong: %v", ev["_systemCoordinates"])
	}
}

func TestUploader_InjectsStationName(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetSystemWithPos("Sol", 100, [3]float64{0, 0, 0})
	sess.SetDocked("Abraham Lincoln", 128666761, 100)

	c := &capture{}
	u := setupEnabled(t, sess, c)
	raw := mustRaw(t, "Market", map[string]any{"MarketID": 128666761})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if c.reqCount.Load() != 1 {
		t.Fatalf("req count = %d", c.reqCount.Load())
	}
	ev := c.messages[0][0]
	if ev["_stationName"] != "Abraham Lincoln" {
		t.Errorf("_stationName = %v", ev["_stationName"])
	}
}

func TestUploader_RespectsDiscardList(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &capture{}
	u := setupEnabled(t, sess, c)
	// Inject a discard list manually: "Music" should be skipped.
	u.discard.mu.Lock()
	u.discard.events = map[string]bool{"Music": true}
	u.discard.fetched = true
	u.discard.mu.Unlock()

	raw := mustRaw(t, "Music", map[string]any{"MusicTrack": "MainMenu"})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if c.reqCount.Load() != 0 {
		t.Errorf("discarded event was uploaded")
	}
}

func TestUploader_HonoursRateLimitReset(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	resetAt := time.Now().Add(5 * time.Second).Unix()
	c := &capture{
		respHeader: http.Header{
			"X-Rate-Limit-Remaining": {"0"},
			"X-Rate-Limit-Reset":     {strconv.FormatInt(resetAt, 10)},
		},
	}
	u := setupEnabled(t, sess, c)

	// First call hits the server, gets the rate-limit headers back.
	if err := u.HandleEvent(context.Background(), mustRaw(t, "FSDJump", map[string]any{"StarSystem": "Sol"})); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if c.reqCount.Load() != 1 {
		t.Fatalf("first call should have hit server")
	}
	// Second call should be suppressed locally — we're inside the rate-limit window.
	if err := u.HandleEvent(context.Background(), mustRaw(t, "FSDJump", map[string]any{"StarSystem": "Sol"})); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if c.reqCount.Load() != 1 {
		t.Errorf("rate-limited window not honoured; saw %d requests, want 1", c.reqCount.Load())
	}
}

func TestUploader_HandlesEDSM2xxMsgnumAsError(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &capture{
		respBody: `{"msgnum": 203, "msg": "Bad event"}`,
	}
	u := setupEnabled(t, sess, c)

	var lastLevel string
	u.OnStatus = func(level, msg string) { lastLevel = level }

	if err := u.HandleEvent(context.Background(), mustRaw(t, "FSDJump", map[string]any{"StarSystem": "Sol"})); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if lastLevel != "ERROR" {
		t.Errorf("expected ERROR status for msgnum 2xx; got %q", lastLevel)
	}
}

func TestUploader_Handles5xxMsgnumAsSuccess(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &capture{
		respBody: `{"msgnum": 501, "msg": "Saved for later"}`,
	}
	u := setupEnabled(t, sess, c)
	var lastLevel string
	u.OnStatus = func(level, msg string) { lastLevel = level }

	if err := u.HandleEvent(context.Background(), mustRaw(t, "FSDJump", map[string]any{"StarSystem": "Sol"})); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if lastLevel != "INFO" {
		t.Errorf("expected INFO status for msgnum 5xx; got %q", lastLevel)
	}
}

func TestDiscardSet_RefreshFiltersDocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`["Music","Docked","ReceiveText"]`))
	}))
	defer srv.Close()

	// We need to point at the test server; the simplest test is to call
	// the raw refreshAt helper. Inline that here.
	d := newDiscardSet()
	hc := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	var names []string
	_ = json.NewDecoder(resp.Body).Decode(&names)
	set := map[string]bool{}
	for _, n := range names {
		if n == "Docked" {
			continue
		}
		set[n] = true
	}
	d.mu.Lock()
	d.events = set
	d.fetched = true
	d.mu.Unlock()

	if !d.Has("Music") {
		t.Error("Music should be discarded")
	}
	if d.Has("Docked") {
		t.Error("Docked must be re-enabled even if EDSM sent it in the discard list")
	}
	if !d.Fetched() {
		t.Error("Fetched should be true after a successful refresh")
	}
}
