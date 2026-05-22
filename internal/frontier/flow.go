package frontier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultClientID is the shared OAuth client_id registered for
// ed-colonization-reporter with Frontier. PKCE makes the client_id
// public-safe; the client_secret ("Shared Key") is intentionally not
// embedded and not needed for the authorization-code-with-PKCE flow.
const DefaultClientID = "97eb517e-ca48-4f90-bb97-e464f299884f"

// DefaultRedirectURI points at the static GitHub Pages redirector that
// forwards the auth code back to a localhost listener. Frontier requires
// https:// and exact-match for the registered redirect_uri, which rules
// out the standard http://localhost:<port> RFC 8252 pattern.
const DefaultRedirectURI = "https://pequalsnp.github.io/ed-colonization-reporter/auth.html"

// flowEntry tracks one in-flight sign-in. We keep the PKCE verifier and a
// CSRF value, both scoped to the lifetime of the user's browser handshake.
type flowEntry struct {
	Verifier  string
	ExpiresAt time.Time
}

// FlowManager owns active sign-in flows. There can usually only be one in
// progress at a time, but we keep a map keyed by CSRF in case the user
// triggers a second attempt before the first finishes.
type FlowManager struct {
	OAuth       *Client
	ClientID    string
	RedirectURI string
	Store       TokenStore

	// OnTokens, if set, is called after a successful Exchange. The wired-up
	// app uses this to update its CAPI client's in-memory token cache so
	// the first request after sign-in doesn't have to re-load from disk.
	OnTokens func(*Tokens)

	// FlowTTL is how long a single sign-in attempt remains valid before
	// the in-memory entry is garbage-collected. Defaults to 10 minutes.
	FlowTTL time.Duration

	mu     sync.Mutex
	active map[string]flowEntry // csrf -> entry
}

// NewFlowManager wires the dependencies for sign-in. ClientID and
// RedirectURI default to the package-level constants when blank.
func NewFlowManager(oauth *Client, store TokenStore) *FlowManager {
	return &FlowManager{
		OAuth:       oauth,
		ClientID:    DefaultClientID,
		RedirectURI: DefaultRedirectURI,
		Store:       store,
		FlowTTL:     10 * time.Minute,
		active:      map[string]flowEntry{},
	}
}

// Start generates a fresh PKCE pair + CSRF, records them, and returns the
// authorization URL the caller should open in the user's browser. The
// localPort is encoded into the OAuth state so the GitHub Pages redirector
// knows which loopback port to forward to.
func (f *FlowManager) Start(localPort int) (string, error) {
	if f.ClientID == "" {
		return "", errors.New("frontier flow: ClientID required")
	}
	if f.RedirectURI == "" {
		return "", errors.New("frontier flow: RedirectURI required")
	}
	if localPort <= 0 || localPort >= 65536 {
		return "", fmt.Errorf("frontier flow: invalid localPort %d", localPort)
	}
	pkce, err := NewPKCE()
	if err != nil {
		return "", err
	}
	csrf, err := NewState()
	if err != nil {
		return "", err
	}
	state, err := encodeState(localPort, csrf)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	f.gcExpiredLocked()
	f.active[csrf] = flowEntry{
		Verifier:  pkce.Verifier,
		ExpiresAt: time.Now().Add(f.flowTTL()),
	}
	f.mu.Unlock()

	return AuthorizeURL(AuthorizeParams{
		ClientID:    f.ClientID,
		RedirectURI: f.RedirectURI,
		Challenge:   pkce.Challenge,
		State:       state,
	})
}

// Complete consumes an authorization-code response from the browser
// callback, validates it against the matching in-memory flow, exchanges
// the code for tokens, and persists them.
func (f *FlowManager) Complete(ctx context.Context, code, state string) (*Tokens, error) {
	if code == "" || state == "" {
		return nil, errors.New("frontier flow: code and state required")
	}
	_, csrf, err := decodeState(state)
	if err != nil {
		return nil, fmt.Errorf("frontier flow: bad state: %w", err)
	}

	f.mu.Lock()
	entry, ok := f.active[csrf]
	if ok {
		delete(f.active, csrf) // one-time use
	}
	f.gcExpiredLocked()
	f.mu.Unlock()

	if !ok {
		return nil, errors.New("frontier flow: unknown or expired state — start sign-in again")
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil, errors.New("frontier flow: state expired — start sign-in again")
	}

	tok, err := f.OAuth.Exchange(ctx, ExchangeParams{
		ClientID:    f.ClientID,
		RedirectURI: f.RedirectURI,
		Code:        code,
		Verifier:    entry.Verifier,
	})
	if err != nil {
		return nil, err
	}
	if err := f.Store.Save(tok); err != nil {
		return nil, fmt.Errorf("frontier flow: persist tokens: %w", err)
	}
	if f.OnTokens != nil {
		f.OnTokens(tok)
	}
	return tok, nil
}

// Pending reports how many in-flight sign-ins there are. Used in tests
// and in the status endpoint.
func (f *FlowManager) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gcExpiredLocked()
	return len(f.active)
}

func (f *FlowManager) gcExpiredLocked() {
	now := time.Now()
	for k, v := range f.active {
		if now.After(v.ExpiresAt) {
			delete(f.active, k)
		}
	}
}

func (f *FlowManager) flowTTL() time.Duration {
	if f.FlowTTL > 0 {
		return f.FlowTTL
	}
	return 10 * time.Minute
}

// statePayload is what we encode into the OAuth `state` parameter. Frontier
// round-trips it through the user's browser; the GitHub Pages redirector
// decodes Port to know where to forward, and the app validates CSRF.
type statePayload struct {
	Port int    `json:"port"`
	CSRF string `json:"csrf"`
}

func encodeState(port int, csrf string) (string, error) {
	buf, err := json.Marshal(statePayload{Port: port, CSRF: csrf})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func decodeState(s string) (port int, csrf string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		// Some browsers may pad the value, accept the standard encoding too.
		raw, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return 0, "", fmt.Errorf("decode state: %w", err)
		}
	}
	var p statePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, "", fmt.Errorf("decode state: %w", err)
	}
	if p.CSRF == "" {
		return 0, "", errors.New("decode state: empty csrf")
	}
	return p.Port, p.CSRF, nil
}
