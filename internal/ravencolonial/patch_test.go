package ravencolonial

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestPatchProject_SendsPATCHWithBuildIDInBody(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	})
	num := 3
	patch := ProjectPatch{FactionName: "Mother Gaia", BodyName: "Sol 3", BodyNum: &num}
	if err := c.PatchProject(context.Background(), "build-abc", patch); err != nil {
		t.Fatalf("PatchProject: %v", err)
	}
	// PATCH, not POST: POST on this path is the full-snapshot verb and a
	// three-field body there risks reading as a replacement.
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/api/project/build-abc" {
		t.Errorf("path = %s", gotPath)
	}
	// buildId is the ProjectUpdate schema's only required field. Sending it
	// in the path but not the body is what made this call 400.
	if gotBody["buildId"] != "build-abc" {
		t.Errorf("body buildId = %v, want build-abc", gotBody["buildId"])
	}
	if gotBody["factionName"] != "Mother Gaia" {
		t.Errorf("body factionName = %v", gotBody["factionName"])
	}
	if gotBody["bodyName"] != "Sol 3" {
		t.Errorf("body bodyName = %v", gotBody["bodyName"])
	}
	if gotBody["bodyNum"] != float64(3) {
		t.Errorf("body bodyNum = %v", gotBody["bodyNum"])
	}
}

func TestPatchProject_PathBuildIDWinsOverBody(t *testing.T) {
	var gotBody map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	})
	patch := ProjectPatch{BuildID: "stale", FactionName: "Mother Gaia"}
	if err := c.PatchProject(context.Background(), "build-abc", patch); err != nil {
		t.Fatalf("PatchProject: %v", err)
	}
	if gotBody["buildId"] != "build-abc" {
		t.Errorf("body buildId = %v, want the path value build-abc", gotBody["buildId"])
	}
}

func TestPatchProject_EmptyPatchIsNoop(t *testing.T) {
	called := false
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	if err := c.PatchProject(context.Background(), "build-abc", ProjectPatch{}); err != nil {
		t.Fatalf("PatchProject empty: %v", err)
	}
	if called {
		t.Error("empty patch should not hit the server")
	}
}

func TestPatchProject_RequiresBuildID(t *testing.T) {
	c := New()
	if err := c.PatchProject(context.Background(), "", ProjectPatch{FactionName: "x"}); err == nil {
		t.Error("want an error when buildID is empty")
	}
}

func TestPatchCarrierCargo_RequiresAPIKey(t *testing.T) {
	c := New()
	if err := c.PatchCarrierCargo(context.Background(), 1, Cargo{"titanium": 1}); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("got %v, want ErrNoAPIKey", err)
	}
}

func TestPatchCarrierCargo_EmptyDeltaIsNoop(t *testing.T) {
	called := false
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	c = New(WithBaseURL(c.baseURL), WithAPIKey("k"))
	if err := c.PatchCarrierCargo(context.Background(), 42, Cargo{}); err != nil {
		t.Fatalf("PatchCarrierCargo empty: %v", err)
	}
	if called {
		t.Error("empty delta should not hit the server")
	}
}

func TestPatchCarrierCargo_SendsPATCH(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   Cargo
	)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	})
	c = New(WithBaseURL(c.baseURL), WithAPIKey("k"))
	delta := Cargo{"cmmcomposite": 2464, "titanium": -100}
	if err := c.PatchCarrierCargo(context.Background(), 3700000123, delta); err != nil {
		t.Fatalf("PatchCarrierCargo: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/api/fc/3700000123/cargo" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["cmmcomposite"] != 2464 || gotBody["titanium"] != -100 {
		t.Errorf("body = %+v", gotBody)
	}
}
