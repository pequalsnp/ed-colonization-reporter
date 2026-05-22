package frontier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newCAPITest(t *testing.T, handler http.HandlerFunc, store TokenStore) (*CAPI, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	oauth := NewClient()
	oauth.TokenEndpoint = srv.URL + "/__token" // unused unless refresh-path tested
	c := NewCAPI(oauth, "client-id", store)
	c.Host = srv.URL
	c.HTTPClient = srv.Client()
	return c, srv
}

func TestFleetCarrier_DecodesCargo(t *testing.T) {
	store := &MemoryTokenStore{}
	_ = store.Save(&Tokens{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)})

	c, _ := newCAPITest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fleetcarrier" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer AT" {
			t.Errorf("auth = %s", auth)
		}
		_, _ = w.Write([]byte(`{
			"market_id": 3700000123,
			"name": {"filteredVanityName": "MY-FC", "callsign": "ABC-12X"},
			"currentStarSystem": "Sol",
			"cargo": [
				{"commodity": "cmmcomposite", "qty": 2464, "originSystem": 100},
				{"commodity": "titanium", "qty": 78}
			]
		}`))
	}, store)

	fc, err := c.FleetCarrier(context.Background())
	if err != nil {
		t.Fatalf("FleetCarrier: %v", err)
	}
	if fc.MarketID != 3700000123 {
		t.Errorf("MarketID = %d", fc.MarketID)
	}
	if fc.Name.Callsign != "ABC-12X" {
		t.Errorf("Callsign = %s", fc.Name.Callsign)
	}
	if len(fc.Cargo) != 2 {
		t.Fatalf("cargo len = %d", len(fc.Cargo))
	}
	if fc.Cargo[0].Commodity != "cmmcomposite" || fc.Cargo[0].Quantity != 2464 {
		t.Errorf("first cargo = %+v", fc.Cargo[0])
	}
}

func TestFleetCarrier_HonoursCooldown(t *testing.T) {
	store := &MemoryTokenStore{}
	_ = store.Save(&Tokens{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)})

	hits := 0
	c, _ := newCAPITest(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"market_id":1,"cargo":[]}`))
	}, store)

	if _, err := c.FleetCarrier(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := c.FleetCarrier(context.Background()); !errors.Is(err, ErrFleetCarrierRateLimited) {
		t.Errorf("second call err = %v, want ErrFleetCarrierRateLimited", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

func TestFleetCarrier_RetriesOn401Refresh(t *testing.T) {
	store := &MemoryTokenStore{}
	_ = store.Save(&Tokens{AccessToken: "STALE", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)})

	var (
		calls       int
		refreshHits int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/fleetcarrier", func(w http.ResponseWriter, r *http.Request) {
		calls++
		auth := r.Header.Get("Authorization")
		if auth == "Bearer STALE" {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		if auth == "Bearer FRESH" {
			_, _ = w.Write([]byte(`{"market_id":1,"cargo":[]}`))
			return
		}
		t.Errorf("unexpected auth header: %q", auth)
	})
	mux.HandleFunc("/__token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits++
		_, _ = w.Write([]byte(`{"access_token":"FRESH","refresh_token":"RT2","expires_in":3600,"token_type":"Bearer"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	oauth := NewClient()
	oauth.TokenEndpoint = srv.URL + "/__token"
	c := NewCAPI(oauth, "client-id", store)
	c.Host = srv.URL
	c.HTTPClient = srv.Client()

	fc, err := c.FleetCarrier(context.Background())
	if err != nil {
		t.Fatalf("FleetCarrier: %v", err)
	}
	if fc.MarketID != 1 {
		t.Errorf("MarketID = %d", fc.MarketID)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (stale + fresh)", calls)
	}
	if refreshHits != 1 {
		t.Errorf("refresh hits = %d, want 1", refreshHits)
	}
	// Rotated refresh token must be persisted.
	stored, _ := store.Load()
	if stored.RefreshToken != "RT2" {
		t.Errorf("refresh token not rotated; got %q", stored.RefreshToken)
	}
}

func TestFleetCarrier_RefreshesWhenExpired(t *testing.T) {
	store := &MemoryTokenStore{}
	_ = store.Save(&Tokens{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(-1 * time.Hour)})

	mux := http.NewServeMux()
	mux.HandleFunc("/fleetcarrier", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer FRESH" {
			t.Errorf("expected refreshed token, got %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"market_id":1,"cargo":[]}`))
	})
	mux.HandleFunc("/__token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"FRESH","refresh_token":"RT","expires_in":3600,"token_type":"Bearer"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	oauth := NewClient()
	oauth.TokenEndpoint = srv.URL + "/__token"
	c := NewCAPI(oauth, "client-id", store)
	c.Host = srv.URL
	c.HTTPClient = srv.Client()

	if _, err := c.FleetCarrier(context.Background()); err != nil {
		t.Fatalf("FleetCarrier: %v", err)
	}
}

func TestFleetCarrier_NoTokensSurfacesError(t *testing.T) {
	store := &MemoryTokenStore{}
	c, _ := newCAPITest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called when there are no tokens")
	}, store)
	_, err := c.FleetCarrier(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "load tokens") {
		t.Errorf("err = %v", err)
	}
}

func TestHasTokens(t *testing.T) {
	store := &MemoryTokenStore{}
	c := NewCAPI(NewClient(), "cid", store)
	if c.HasTokens(context.Background()) {
		t.Error("empty store should report no tokens")
	}
	_ = store.Save(&Tokens{AccessToken: "x"})
	if !c.HasTokens(context.Background()) {
		t.Error("after save, HasTokens should be true")
	}
}

func TestCommodityKey(t *testing.T) {
	cases := map[string]string{
		"cmmcomposite":   "cmmcomposite",
		"CMM Composite":  "cmm composite",
		"  Titanium  ":   "titanium",
		"":               "",
	}
	for in, want := range cases {
		if got := CommodityKey(in); got != want {
			t.Errorf("CommodityKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFleetCarrier_DecodesEmptyCargoArray pins the case where the FC has
// no cargo — the response has `"cargo": []` and Quantity stays zero.
func TestFleetCarrier_DecodesEmptyCargoArray(t *testing.T) {
	store := &MemoryTokenStore{}
	_ = store.Save(&Tokens{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)})
	c, _ := newCAPITest(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{"market_id": 1, "cargo": []any{}})
		_, _ = w.Write(body)
	}, store)
	fc, err := c.FleetCarrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.Cargo) != 0 {
		t.Errorf("expected empty cargo; got %v", fc.Cargo)
	}
}
