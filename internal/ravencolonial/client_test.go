package ravencolonial

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(WithBaseURL(srv.URL)), srv
}

func TestProjectBySystemMarket_OK(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		want := "/api/system/123456789/3789012345"
		if r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		_ = json.NewEncoder(w).Encode(Project{BuildID: "abc-123", SystemName: "Sol"})
	})
	p, err := c.ProjectBySystemMarket(context.Background(), 123456789, 3789012345)
	if err != nil {
		t.Fatalf("ProjectBySystemMarket: %v", err)
	}
	if p.BuildID != "abc-123" || p.SystemName != "Sol" {
		t.Errorf("got %+v", p)
	}
}

func TestProjectBySystemMarket_404IsNotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	_, err := c.ProjectBySystemMarket(context.Background(), 1, 2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound = false, want true (err=%v)", err)
	}
}

func TestUpdateProject_PostsJSON(t *testing.T) {
	var got ProjectUpdate
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/project/build-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("server unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	want := ProjectUpdate{
		BuildID:     "build-1",
		Commodities: map[string]int{"titanium": 100, "steel": 50},
		MaxNeed:     150,
	}
	if err := c.UpdateProject(context.Background(), want); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if got.BuildID != "build-1" || got.MaxNeed != 150 {
		t.Errorf("server received %+v", got)
	}
	if got.Commodities["titanium"] != 100 {
		t.Errorf("titanium = %d, want 100", got.Commodities["titanium"])
	}
}

func TestUpdateProject_RequiresBuildID(t *testing.T) {
	c := New()
	if err := c.UpdateProject(context.Background(), ProjectUpdate{}); err == nil {
		t.Error("expected error for empty BuildID")
	}
}

func TestCompleteProject(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/project/build-1/complete" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.CompleteProject(context.Background(), "build-1"); err != nil {
		t.Fatalf("CompleteProject: %v", err)
	}
}

func TestContribute(t *testing.T) {
	var got Contribution
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		want := "/api/project/build-1/contribute/Cmdr%20Test"
		if r.URL.EscapedPath() != want {
			t.Errorf("path = %s, want %s", r.URL.EscapedPath(), want)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	})
	if err := c.Contribute(context.Background(), "build-1", "Cmdr Test", Contribution{"titanium": 32}); err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	if got["titanium"] != 32 {
		t.Errorf("titanium = %d, want 32", got["titanium"])
	}
}

func TestContribute_EmptyMapIsNoop(t *testing.T) {
	called := false
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	if err := c.Contribute(context.Background(), "build-1", "X", Contribution{}); err != nil {
		t.Errorf("Contribute empty: %v", err)
	}
	if called {
		t.Error("server should not have been hit for empty contribution")
	}
}

func TestActiveProjects(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cmdr/Jameson/active" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]Project{
			{BuildID: "b1", BuildName: "Outpost A"},
			{BuildID: "b2", BuildName: "Coriolis B"},
		})
	})
	ps, err := c.ActiveProjects(context.Background(), "Jameson")
	if err != nil {
		t.Fatalf("ActiveProjects: %v", err)
	}
	if len(ps) != 2 || ps[0].BuildID != "b1" {
		t.Errorf("got %+v", ps)
	}
}

func TestAPIKey_HeaderSetWhenConfigured(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("rcc-key")
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	c := New(WithBaseURL(srv.URL), WithAPIKey("secret-key-123"))
	if _, err := c.ProjectBySystemMarket(context.Background(), 1, 2); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got != "secret-key-123" {
		t.Errorf("rcc-key header = %q, want secret-key-123", got)
	}
}

func TestAPIError_FormatsStatusAndBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request: thing was wrong", http.StatusBadRequest)
	})
	err := c.UpdateProject(context.Background(), ProjectUpdate{BuildID: "b1", Commodities: map[string]int{}})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "400") || !strings.Contains(msg, "bad request") {
		t.Errorf("error message %q missing status or body", msg)
	}
}

func TestBaseURL_TrailingSlashTrimmed(t *testing.T) {
	c := New(WithBaseURL("https://example.com/api/"))
	if got := c.baseURL; strings.HasSuffix(got, "/") {
		t.Errorf("baseURL %q should not end with slash", got)
	}
}
