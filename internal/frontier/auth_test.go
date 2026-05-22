package frontier

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeURL_Required(t *testing.T) {
	cases := []AuthorizeParams{
		{},
		{ClientID: "x"},
		{ClientID: "x", RedirectURI: "http://localhost/cb"},
		{ClientID: "x", RedirectURI: "http://localhost/cb", Challenge: "c"},
	}
	for _, p := range cases {
		if _, err := AuthorizeURL(p); err == nil {
			t.Errorf("expected error for %+v", p)
		}
	}
}

func TestAuthorizeURL_AssemblesQuery(t *testing.T) {
	got, err := AuthorizeURL(AuthorizeParams{
		ClientID:    "client-123",
		RedirectURI: "http://localhost:38421/auth",
		Challenge:   "abc123",
		State:       "state-xyz",
	})
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "auth.frontierstore.net" {
		t.Errorf("host = %s", u.Host)
	}
	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "client-123",
		"redirect_uri":          "http://localhost:38421/auth",
		"scope":                 "auth capi",
		"audience":              "frontier,steam,epic",
		"state":                 "state-xyz",
		"code_challenge":        "abc123",
		"code_challenge_method": "S256",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("query[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestAuthorizeURL_OverrideScopesAndAudience(t *testing.T) {
	got, _ := AuthorizeURL(AuthorizeParams{
		ClientID:    "c",
		RedirectURI: "http://localhost/cb",
		Challenge:   "c",
		State:       "s",
		Scopes:      []string{"capi", "auth", "extra"},
		Audience:    "epic",
	})
	u, _ := url.Parse(got)
	if u.Query().Get("scope") != "capi auth extra" {
		t.Errorf("scope = %q", u.Query().Get("scope"))
	}
	if u.Query().Get("audience") != "epic" {
		t.Errorf("audience = %q", u.Query().Get("audience"))
	}
}

func newTokenServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.TokenEndpoint = srv.URL
	return c, srv
}

func TestExchange_SendsExpectedForm(t *testing.T) {
	var got url.Values
	c, _ := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %s", ct)
		}
		body, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(body))
		_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","token_type":"Bearer","expires_in":14400,"scope":"auth capi"}`))
	})
	tok, err := c.Exchange(context.Background(), ExchangeParams{
		ClientID: "cid", Code: "code-abc", Verifier: "ver", RedirectURI: "http://localhost/cb",
	})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "cid",
		"code":          "code-abc",
		"code_verifier": "ver",
		"redirect_uri":  "http://localhost/cb",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("form[%q] = %q, want %q", k, got.Get(k), v)
		}
	}
	if tok.AccessToken != "AT" || tok.RefreshToken != "RT" || tok.TokenType != "Bearer" {
		t.Errorf("token = %+v", tok)
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be set from expires_in")
	}
}

func TestExchange_NonOKReturnsTokenError(t *testing.T) {
	c, _ := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_grant","error_description":"bad code"}`, http.StatusBadRequest)
	})
	_, err := c.Exchange(context.Background(), ExchangeParams{
		ClientID: "cid", Code: "x", Verifier: "v", RedirectURI: "http://localhost/cb",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var te *TokenError
	if !errors.As(err, &te) {
		t.Fatalf("err type = %T, want *TokenError", err)
	}
	if te.StatusCode != 400 {
		t.Errorf("StatusCode = %d", te.StatusCode)
	}
	if !strings.Contains(te.Error(), "400") {
		t.Errorf("Error() = %q", te.Error())
	}
}

func TestRefresh_SendsForm(t *testing.T) {
	var got url.Values
	c, _ := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(body))
		_, _ = w.Write([]byte(`{"access_token":"NEW_AT","refresh_token":"NEW_RT","expires_in":14400,"token_type":"Bearer"}`))
	})
	tok, err := c.Refresh(context.Background(), "cid", "old-rt")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", got.Get("grant_type"))
	}
	if got.Get("refresh_token") != "old-rt" {
		t.Errorf("refresh_token = %q", got.Get("refresh_token"))
	}
	if tok.AccessToken != "NEW_AT" || tok.RefreshToken != "NEW_RT" {
		t.Errorf("token rotation: %+v", tok)
	}
}

func TestTokensExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		tok  *Tokens
		want bool
	}{
		{"nil", nil, true},
		{"zero ExpiresAt", &Tokens{AccessToken: "x"}, true},
		{"future", &Tokens{ExpiresAt: now.Add(2 * time.Hour)}, false},
		{"already past", &Tokens{ExpiresAt: now.Add(-time.Hour)}, true},
		{"within skew", &Tokens{ExpiresAt: now.Add(30 * time.Second)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tok.Expired(); got != tc.want {
				t.Errorf("Expired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecode_BearerHeader(t *testing.T) {
	c, srv := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		// We reuse newTokenServer for convenience; redirect decode here too.
		if auth := r.Header.Get("Authorization"); auth != "Bearer AT-123" {
			t.Errorf("Authorization header = %q", auth)
		}
		_, _ = w.Write([]byte(`{"usr":{"customer_id":"42","email":"x@y","name":"Jameson"}}`))
	})
	c.DecodeEndpoint = srv.URL
	d, err := c.Decode(context.Background(), "AT-123")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if d.Usr.Name != "Jameson" {
		t.Errorf("name = %q", d.Usr.Name)
	}
}

func TestExchange_InjectedNowUsedForExpiresAt(t *testing.T) {
	c, _ := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"AT","expires_in":100,"token_type":"Bearer"}`))
	})
	fixed := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return fixed }
	tok, err := c.Exchange(context.Background(), ExchangeParams{
		ClientID: "c", Code: "x", Verifier: "v", RedirectURI: "http://localhost/cb",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := fixed.Add(100 * time.Second)
	if !tok.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", tok.ExpiresAt, want)
	}
}
