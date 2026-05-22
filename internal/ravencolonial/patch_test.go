package ravencolonial

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

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
