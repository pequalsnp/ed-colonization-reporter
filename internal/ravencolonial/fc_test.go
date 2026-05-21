package ravencolonial

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestPutFleetCarrier_RequiresAPIKey(t *testing.T) {
	c := New() // no key
	err := c.PutFleetCarrier(context.Background(), FleetCarrier{MarketID: 1, Name: "x"})
	if !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("got %v, want ErrNoAPIKey", err)
	}
}

func TestPutFleetCarrier_PostsExpectedBody(t *testing.T) {
	var got FleetCarrier
	headerKey := ""
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/fc/3700000123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		headerKey = r.Header.Get("rcc-key")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	})
	c = New(WithBaseURL(c.baseURL), WithAPIKey("k-1"))

	fc := FleetCarrier{
		MarketID: 3700000123, Name: "DREAMSTRIDER", Callsign: "ABC-12X",
		StarSystem: "Sol", SystemAddress: 10477373803,
	}
	if err := c.PutFleetCarrier(context.Background(), fc); err != nil {
		t.Fatalf("PutFleetCarrier: %v", err)
	}
	if headerKey != "k-1" {
		t.Errorf("rcc-key = %q", headerKey)
	}
	if got != fc {
		t.Errorf("body = %+v, want %+v", got, fc)
	}
}

func TestOverwriteCarrierCargo_RequiresAPIKey(t *testing.T) {
	c := New()
	if err := c.OverwriteCarrierCargo(context.Background(), 1, Cargo{"titanium": 10}); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("got %v, want ErrNoAPIKey", err)
	}
}

func TestOverwriteCarrierCargo_PostsMap(t *testing.T) {
	var got Cargo
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/fc/42/cargo" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	})
	c = New(WithBaseURL(c.baseURL), WithAPIKey("k-1"))
	want := Cargo{"titanium": 420, "steel": 100}
	if err := c.OverwriteCarrierCargo(context.Background(), 42, want); err != nil {
		t.Fatalf("OverwriteCarrierCargo: %v", err)
	}
	if got["titanium"] != 420 || got["steel"] != 100 {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOverwriteCarrierCargo_NilSendsEmpty(t *testing.T) {
	var got Cargo
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	})
	c = New(WithBaseURL(c.baseURL), WithAPIKey("k"))
	if err := c.OverwriteCarrierCargo(context.Background(), 42, nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}
