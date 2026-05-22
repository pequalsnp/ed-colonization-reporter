package frontier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Companion API host constants. The Live galaxy is what we care about for
// colonization (Trailblazers and onward); Legacy/Beta are kept for future
// gameversion-based routing.
const (
	CAPILive   = "https://companion.orerve.net"
	CAPILegacy = "https://legacy-companion.orerve.net"
	CAPIBeta   = "https://pts-companion.orerve.net"
)

// Documented per-endpoint cooldowns from Frontier (mirroring EDMC's
// observations). The /fleetcarrier endpoint refreshes server-side only on
// the player's dock events, so polling more often is wasteful.
const (
	FleetCarrierCooldown = 15 * time.Minute
)

// CAPI is the Bearer-authenticated client for Frontier's Companion API.
// It holds a reference to an OAuth Client and TokenStore and refreshes
// tokens transparently on 401.
type CAPI struct {
	HTTPClient *http.Client
	Host       string // defaults to CAPILive

	OAuth    *Client
	ClientID string
	Store    TokenStore

	mu          sync.Mutex
	lastFC      time.Time
	tokenMemo   *Tokens
}

// NewCAPI builds a CAPI client. Pass the OAuth client used for refresh, the
// shared client_id, and a TokenStore for persistent refresh-token rotation.
func NewCAPI(oauth *Client, clientID string, store TokenStore) *CAPI {
	return &CAPI{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Host:       CAPILive,
		OAuth:      oauth,
		ClientID:   clientID,
		Store:      store,
	}
}

// FleetCarrierCargoItem matches one row of the /fleetcarrier response's
// cargo array. We only model the fields we need; cAPI's full shape is
// large and we'd rather ignore unknown fields than chase Frontier-side
// drift.
//
// Note: cAPI also returns a `mission` field on each cargo row that is
// EITHER a mission ID (number) OR boolean false when the cargo isn't
// mission-tied. We don't use the field for anything, so we don't model
// it — Go's encoding/json silently ignores keys with no destination.
type FleetCarrierCargoItem struct {
	Commodity    string `json:"commodity"`
	OriginSystem int64  `json:"originSystem,omitempty"`
	Stolen       bool   `json:"stolen,omitempty"`
	Quantity     int    `json:"qty"`
	Value        int    `json:"value,omitempty"`
	LocName      string `json:"locName,omitempty"`
}

// FleetCarrier is the parsed subset of /fleetcarrier we use. Frontier
// returns much more (modules, services, finance, …); we keep only what
// drives cargo sync.
type FleetCarrier struct {
	Name struct {
		Filtered string `json:"filteredVanityName"`
		Callsign string `json:"callsign"`
	} `json:"name"`
	MarketID         int64                   `json:"market_id"`
	CurrentStarSystem string                 `json:"currentStarSystem"`
	Cargo            []FleetCarrierCargoItem `json:"cargo"`
}

// FleetCarrier fetches the current commander's FC. Returns ErrFleetCarrierRateLimited
// if called more often than FleetCarrierCooldown — Frontier enforces this
// server-side and clients are expected to throttle themselves.
func (c *CAPI) FleetCarrier(ctx context.Context) (*FleetCarrier, error) {
	c.mu.Lock()
	since := time.Since(c.lastFC)
	c.mu.Unlock()
	if since < FleetCarrierCooldown && !c.lastFC.IsZero() {
		return nil, ErrFleetCarrierRateLimited
	}

	body, err := c.get(ctx, "/fleetcarrier")
	if err != nil {
		return nil, err
	}
	var fc FleetCarrier
	if err := json.Unmarshal(body, &fc); err != nil {
		return nil, fmt.Errorf("frontier capi: decode /fleetcarrier: %w", err)
	}

	c.mu.Lock()
	c.lastFC = time.Now()
	c.mu.Unlock()
	return &fc, nil
}

// ErrFleetCarrierRateLimited is returned when a /fleetcarrier call would
// have violated the documented 15-minute cooldown.
var ErrFleetCarrierRateLimited = errors.New("frontier capi: /fleetcarrier cooldown active (15 min)")

// get performs a Bearer-authenticated GET to host+path with one retry
// after refreshing tokens on a 401.
func (c *CAPI) get(ctx context.Context, path string) ([]byte, error) {
	tok, err := c.token(ctx, false)
	if err != nil {
		return nil, err
	}
	body, status, err := c.doRequest(ctx, tok.AccessToken, path)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		// Refresh + retry once.
		tok, err = c.token(ctx, true)
		if err != nil {
			return nil, err
		}
		body, status, err = c.doRequest(ctx, tok.AccessToken, path)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status >= 300 {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return nil, fmt.Errorf("frontier capi: %s: HTTP %d: %s", path, status, snippet)
	}
	return body, nil
}

func (c *CAPI) doRequest(ctx context.Context, token, path string) ([]byte, int, error) {
	host := c.Host
	if host == "" {
		host = CAPILive
	}
	url := host + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("frontier capi: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // /fleetcarrier can be a few MB
	return body, resp.StatusCode, nil
}

// token returns a valid access token, refreshing if expired. forceRefresh
// is set after a 401 to retry once with a freshly-rotated token.
func (c *CAPI) token(ctx context.Context, forceRefresh bool) (*Tokens, error) {
	c.mu.Lock()
	tok := c.tokenMemo
	c.mu.Unlock()
	if tok == nil {
		loaded, err := c.Store.Load()
		if err != nil {
			return nil, fmt.Errorf("frontier capi: load tokens: %w", err)
		}
		tok = loaded
	}
	if forceRefresh || tok.Expired() {
		if tok.RefreshToken == "" {
			return nil, errors.New("frontier capi: tokens are expired and no refresh_token is available; sign in again")
		}
		refreshed, err := c.OAuth.Refresh(ctx, c.ClientID, tok.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("frontier capi: refresh: %w", err)
		}
		// Frontier sometimes returns a new refresh token; if not, retain the old one.
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = tok.RefreshToken
		}
		if err := c.Store.Save(refreshed); err != nil {
			return nil, fmt.Errorf("frontier capi: save tokens: %w", err)
		}
		tok = refreshed
	}
	c.mu.Lock()
	c.tokenMemo = tok
	c.mu.Unlock()
	return tok, nil
}

// SetTokens primes the in-memory token cache without touching the store.
// Used by the OAuth-flow handler after a fresh sign-in; the caller has
// already persisted via the store.
func (c *CAPI) SetTokens(t *Tokens) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokenMemo = t
}

// HasTokens reports whether we have any tokens at all (signed in or not).
func (c *CAPI) HasTokens(ctx context.Context) bool {
	if _, err := c.Store.Load(); err == nil {
		return true
	}
	return false
}

// CommodityKey returns the lowercase symbol form of a cAPI commodity name.
// cAPI returns names already in the `cmmcomposite` / `hydrogen_fuel` style,
// but some are uppercase or have spaces — normalise so callers can map to
// the form ravencolonial expects.
func CommodityKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
