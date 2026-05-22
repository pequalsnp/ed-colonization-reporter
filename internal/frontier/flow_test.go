package frontier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEncodeDecodeState_Roundtrip(t *testing.T) {
	s, err := encodeState(38421, "abc-xyz")
	if err != nil {
		t.Fatal(err)
	}
	// Must be URL-safe (no '+', '/', '=').
	if strings.ContainsAny(s, "+/=") {
		t.Errorf("state %q contains non-url-safe chars", s)
	}
	port, csrf, err := decodeState(s)
	if err != nil {
		t.Fatal(err)
	}
	if port != 38421 || csrf != "abc-xyz" {
		t.Errorf("got port=%d csrf=%q", port, csrf)
	}
}

func TestDecodeState_PaddedBase64Accepted(t *testing.T) {
	// Some browsers / intermediaries may pass the standard-padding base64
	// form back to us. Accept it.
	raw, _ := json.Marshal(statePayload{Port: 1, CSRF: "x"})
	padded := base64.URLEncoding.EncodeToString(raw)
	if _, _, err := decodeState(padded); err != nil {
		t.Errorf("padded base64url should decode; got %v", err)
	}
}

func TestDecodeState_RejectsEmptyCSRF(t *testing.T) {
	raw, _ := json.Marshal(statePayload{Port: 1, CSRF: ""})
	s := base64.RawURLEncoding.EncodeToString(raw)
	if _, _, err := decodeState(s); err == nil {
		t.Error("empty csrf must be rejected")
	}
}

func TestFlowStart_BuildsAuthURLAndRemembersVerifier(t *testing.T) {
	f := NewFlowManager(NewClient(), &MemoryTokenStore{})
	u, err := f.Start(38421)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.HasPrefix(u, AuthURL+"?") {
		t.Errorf("URL %q does not target the auth endpoint", u)
	}
	if !strings.Contains(u, "client_id=97eb517e-ca48-4f90-bb97-e464f299884f") {
		t.Errorf("URL missing baked-in client_id; got %q", u)
	}
	if !strings.Contains(u, "code_challenge_method=S256") {
		t.Errorf("URL missing PKCE marker; got %q", u)
	}
	if f.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", f.Pending())
	}
}

func TestFlowStart_RejectsInvalidPort(t *testing.T) {
	f := NewFlowManager(NewClient(), &MemoryTokenStore{})
	cases := []int{0, -1, 65536, 1 << 20}
	for _, p := range cases {
		if _, err := f.Start(p); err == nil {
			t.Errorf("Start(%d) should fail", p)
		}
	}
}

func TestFlowComplete_HappyPath(t *testing.T) {
	store := &MemoryTokenStore{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":14400,"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	oauth := NewClient()
	oauth.TokenEndpoint = srv.URL

	f := NewFlowManager(oauth, store)
	var onTokensCalled *Tokens
	f.OnTokens = func(t *Tokens) { onTokensCalled = t }

	u, err := f.Start(38421)
	if err != nil {
		t.Fatal(err)
	}
	// Extract the state from the URL so we can simulate the redirect.
	state := extractQueryParam(t, u, "state")
	tok, err := f.Complete(context.Background(), "fresh-code", state)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if tok.AccessToken != "AT" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	// Persisted.
	saved, err := store.Load()
	if err != nil || saved.AccessToken != "AT" {
		t.Errorf("store after Complete: %+v err=%v", saved, err)
	}
	// OnTokens fired.
	if onTokensCalled == nil || onTokensCalled.AccessToken != "AT" {
		t.Error("OnTokens callback not invoked with new tokens")
	}
	// Flow entry consumed (one-time use).
	if f.Pending() != 0 {
		t.Errorf("Pending = %d after Complete, want 0", f.Pending())
	}
}

func TestFlowComplete_RejectsUnknownState(t *testing.T) {
	f := NewFlowManager(NewClient(), &MemoryTokenStore{})
	// State is well-formed but never registered.
	state, _ := encodeState(1234, "never-issued")
	_, err := f.Complete(context.Background(), "code", state)
	if err == nil || !strings.Contains(err.Error(), "unknown or expired") {
		t.Errorf("got %v, want unknown-or-expired error", err)
	}
}

func TestFlowComplete_RejectsExpiredState(t *testing.T) {
	f := NewFlowManager(NewClient(), &MemoryTokenStore{})
	f.FlowTTL = 1 * time.Millisecond
	u, _ := f.Start(38421)
	state := extractQueryParam(t, u, "state")
	time.Sleep(20 * time.Millisecond)
	_, err := f.Complete(context.Background(), "code", state)
	if err == nil {
		t.Error("expected expired-state error")
	}
}

func TestFlowComplete_StateOneTime(t *testing.T) {
	store := &MemoryTokenStore{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"AT","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	oauth := NewClient()
	oauth.TokenEndpoint = srv.URL

	f := NewFlowManager(oauth, store)
	u, _ := f.Start(38421)
	state := extractQueryParam(t, u, "state")

	if _, err := f.Complete(context.Background(), "code", state); err != nil {
		t.Fatal(err)
	}
	// Replay with the same state must fail.
	if _, err := f.Complete(context.Background(), "code", state); err == nil {
		t.Error("replayed state should be rejected")
	}
}

func extractQueryParam(t *testing.T, urlStr, key string) string {
	t.Helper()
	i := strings.Index(urlStr, "?")
	if i < 0 {
		t.Fatalf("no query string in %q", urlStr)
	}
	for _, pair := range strings.Split(urlStr[i+1:], "&") {
		if eq := strings.Index(pair, "="); eq > 0 && pair[:eq] == key {
			v, err := urlDecode(pair[eq+1:])
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	t.Fatalf("no %q in %q", key, urlStr)
	return ""
}

func urlDecode(s string) (string, error) {
	// std lib has url.QueryUnescape but we already import enough.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '+':
			out = append(out, ' ')
		case s[i] == '%' && i+2 < len(s):
			b, err := hex2(s[i+1], s[i+2])
			if err != nil {
				return "", err
			}
			out = append(out, b)
			i += 2
		default:
			out = append(out, s[i])
		}
	}
	return string(out), nil
}

func hex2(a, b byte) (byte, error) {
	hex := func(c byte) (byte, error) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', nil
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, nil
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, nil
		}
		return 0, jsonError("bad hex byte " + string(c))
	}
	hi, err := hex(a)
	if err != nil {
		return 0, err
	}
	lo, err := hex(b)
	if err != nil {
		return 0, err
	}
	return hi<<4 | lo, nil
}

func jsonError(s string) error { return &jsErr{s: s} }

type jsErr struct{ s string }

func (e *jsErr) Error() string { return e.s }
