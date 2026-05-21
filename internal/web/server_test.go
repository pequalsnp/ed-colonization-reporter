package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// newTestServer builds a Server with its session/reporter wired up but
// without binding a real listener. Returns an httptest.Server pointed at
// the muxed routes so each test gets isolated state.
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate config.Save in handler tests

	s := New(config.Config{APIBaseURL: ravencolonial.DefaultBaseURL})
	s.Version = "test"
	s.hub = newStatusHub()
	s.session = state.New()
	// Point the API client at an httptest server later if needed; for tests
	// that don't call /api/projects, the upstream URL never gets hit.
	s.client = ravencolonial.New(ravencolonial.WithBaseURL("http://upstream.test"))
	s.rep = reporter.New(s.client, s.session)

	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestRoot_ServesEmbeddedHTML(t *testing.T) {
	_, ts := newTestServer(t)
	r, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status = %d", r.StatusCode)
	}
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), "ED Colonization Reporter") {
		t.Errorf("root page missing app title; body starts with: %q", string(body[:min(200, len(body))]))
	}
}

func TestStatus_ReflectsSession(t *testing.T) {
	s, ts := newTestServer(t)
	s.session.SetCommander("Jameson", "F1")
	s.session.SetSystem("Sol", 100)
	s.session.SetDocked("Sol Construction", 200, 100)

	r, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(r.Body).Decode(&got)
	if got["commander"] != "Jameson" || got["starSystem"] != "Sol" || got["docked"] != true {
		t.Errorf("status response wrong: %+v", got)
	}
	if got["version"] != "test" {
		t.Errorf("version = %v, want test", got["version"])
	}
}

func TestStatus_RejectsPost(t *testing.T) {
	_, ts := newTestServer(t)
	r, err := http.Post(ts.URL+"/api/status", "application/json", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", r.StatusCode)
	}
}

func TestConfig_GetReturnsCurrent(t *testing.T) {
	s, ts := newTestServer(t)
	s.cfg.APIKey = "my-key"
	s.cfg.CommanderOverride = "Alt"

	r, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got configDTO
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "my-key" || got.CommanderOverride != "Alt" {
		t.Errorf("got %+v", got)
	}
}

func TestConfig_PostUpdatesAndPersists(t *testing.T) {
	s, ts := newTestServer(t)

	body, _ := json.Marshal(configDTO{
		JournalDir:        "/x/y",
		APIBaseURL:        "https://example.com",
		APIKey:            "k",
		CommanderOverride: "Alt",
		ReplaySession:     true,
	})
	r, err := http.Post(ts.URL+"/api/config", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", r.StatusCode)
	}
	// Server in-memory state should reflect the new config.
	if s.cfg.APIKey != "k" || !s.cfg.ReplaySession {
		t.Errorf("in-memory cfg not updated: %+v", s.cfg)
	}
	// And the file should have been written under the redirected XDG path.
	r2, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var got configDTO
	if err := json.NewDecoder(r2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.JournalDir != "/x/y" || !got.ReplaySession {
		t.Errorf("roundtripped cfg wrong: %+v", got)
	}
	// The commander override should have been applied to the session too.
	if s.session.Commander() != "Alt" {
		t.Errorf("commander override not applied to session, got %q", s.session.Commander())
	}
}

func TestConfig_PostBadJSON(t *testing.T) {
	_, ts := newTestServer(t)
	r, err := http.Post(ts.URL+"/api/config", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", r.StatusCode)
	}
}

func TestConfig_PostFillsAPIBaseDefault(t *testing.T) {
	s, ts := newTestServer(t)
	body, _ := json.Marshal(configDTO{APIBaseURL: ""}) // explicitly blank
	r, err := http.Post(ts.URL+"/api/config", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if s.cfg.APIBaseURL != ravencolonial.DefaultBaseURL {
		t.Errorf("blank API base should default; got %q", s.cfg.APIBaseURL)
	}
}

func TestProjects_NoCommanderReturnsEmpty(t *testing.T) {
	_, ts := newTestServer(t)
	r, err := http.Get(ts.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), `"projects":[]`) {
		t.Errorf("expected empty projects array; got %s", string(body))
	}
}

func TestProjects_ProxiesUpstream(t *testing.T) {
	s, ts := newTestServer(t)
	s.session.SetCommander("Jameson", "F1")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cmdr/Jameson/active" {
			t.Errorf("upstream got path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"buildId":"b1","systemName":"Sol","commodities":{"titanium":100}}]`))
	}))
	defer upstream.Close()
	s.client = ravencolonial.New(ravencolonial.WithBaseURL(upstream.URL))

	r, err := http.Get(ts.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), `"buildId":"b1"`) || !strings.Contains(string(body), `"commander":"Jameson"`) {
		t.Errorf("got %s", string(body))
	}
}

func TestEvents_StreamsStatus(t *testing.T) {
	s, ts := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}

	// Give the subscriber goroutine a moment to register before publishing.
	time.Sleep(50 * time.Millisecond)
	s.hub.Publish(reporter.Status{Time: time.Now(), Level: reporter.LevelOK, Message: "hello world"})

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if strings.Contains(string(buf[:n]), "hello world") {
					got <- string(buf[:n])
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	select {
	case payload := <-got:
		if !strings.Contains(payload, "event: status") {
			t.Errorf("missing event type marker; payload=%q", payload)
		}
		if !strings.Contains(payload, `"level":"OK"`) {
			t.Errorf("missing level field; payload=%q", payload)
		}
	case <-ctx.Done():
		t.Error("did not receive published status within timeout")
	}
}

func TestStatusHub_DropsSlowSubscribers(t *testing.T) {
	hub := newStatusHub()
	_, cancel := hub.Subscribe()
	defer cancel()
	// Fill far beyond the subscriber's buffer; should not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			hub.Publish(reporter.Status{Message: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}
}

func TestStatusHub_ReplaysBacklog(t *testing.T) {
	hub := newStatusHub()
	hub.Publish(reporter.Status{Message: "before-1"})
	hub.Publish(reporter.Status{Message: "before-2"})
	ch, cancel := hub.Subscribe()
	defer cancel()

	got := []string{}
	deadline := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case s := <-ch:
			got = append(got, s.Message)
		case <-deadline:
			t.Fatalf("only got %d of 2 backlog items: %v", len(got), got)
		}
	}
	if got[0] != "before-1" || got[1] != "before-2" {
		t.Errorf("backlog order wrong: %v", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
