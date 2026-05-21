package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]reporter.Level{
		"OK":      reporter.LevelOK,
		"WARN":    reporter.LevelWarn,
		"ERROR":   reporter.LevelError,
		"INFO":    reporter.LevelInfo,
		"unknown": reporter.LevelInfo,
		"":        reporter.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestResolveJournalDir_Configured(t *testing.T) {
	dir := t.TempDir()
	if got := resolveJournalDir(dir); got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestResolveJournalDir_EmptyFallsBackToDetectOrEmpty(t *testing.T) {
	// Redirect HOME to an empty dir so the journal detection returns
	// nothing — resolveJournalDir should then return "".
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	got := resolveJournalDir("")
	if got != "" {
		// Detection succeeded somehow — fine, just sanity-check shape.
		if !strings.Contains(got, "Elite") {
			t.Errorf("detected dir %q doesn't look like a journal dir", got)
		}
	}
}

func TestServer_StartListensAndServes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate config writes

	srv := New(config.Config{APIBaseURL: ravencolonial.DefaultBaseURL})
	srv.Version = "test"
	srv.Bind = "127.0.0.1:0"

	urlCh := make(chan string, 1)
	srv.OpenBrowser = func(u string) { urlCh <- u }

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	var url string
	select {
	case url = <-urlCh:
	case <-time.After(2 * time.Second):
		cancel()
		<-errCh
		t.Fatal("OpenBrowser never called; Start may not have bound listener")
	}

	// Verify URL is non-empty and points at a 127.0.0.1 address.
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("URL %q does not look like a loopback bind", url)
	}
	if srv.URL() != url {
		t.Errorf("URL() %q != callback url %q", srv.URL(), url)
	}

	// Hit /api/status to confirm the server is actually serving.
	resp, err := http.Get(url + "/api/status")
	if err != nil {
		cancel()
		<-errCh
		t.Fatalf("GET /api/status: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"version":"test"`) {
		t.Errorf("status body missing version: %s", body)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("Start returned %v on shutdown; want nil", err)
	}
}

func TestServer_URLEmptyBeforeStart(t *testing.T) {
	srv := New(config.Config{APIBaseURL: ravencolonial.DefaultBaseURL})
	if got := srv.URL(); got != "" {
		t.Errorf("URL before Start should be empty; got %q", got)
	}
}
