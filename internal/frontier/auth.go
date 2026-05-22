package frontier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Frontier OAuth 2.0 endpoint constants.
const (
	AuthURL   = "https://auth.frontierstore.net/auth"
	TokenURL  = "https://auth.frontierstore.net/token"
	DecodeURL = "https://auth.frontierstore.net/decode"
)

// DefaultScopes are what we always request — `auth` is needed for /decode,
// `capi` is what gets us the Companion API. Requesting `capi` alone has
// historically returned incomplete tokens.
var DefaultScopes = []string{"auth", "capi"}

// DefaultAudience covers Frontier, Steam, and Epic account linkages so the
// flow works regardless of how the user purchased Elite Dangerous.
const DefaultAudience = "frontier,steam,epic"

// AuthorizeParams configure the URL we send the user's browser to.
type AuthorizeParams struct {
	ClientID    string
	RedirectURI string
	Scopes      []string  // defaults to DefaultScopes
	Audience    string    // defaults to DefaultAudience
	Challenge   string    // PKCE code_challenge
	State       string    // CSRF token
}

// AuthorizeURL builds the authorization endpoint URL for the user's browser.
// Returns the URL string or an error if required fields are missing.
func AuthorizeURL(p AuthorizeParams) (string, error) {
	if p.ClientID == "" {
		return "", errors.New("authorize: ClientID required")
	}
	if p.RedirectURI == "" {
		return "", errors.New("authorize: RedirectURI required")
	}
	if p.Challenge == "" {
		return "", errors.New("authorize: Challenge required")
	}
	if p.State == "" {
		return "", errors.New("authorize: State required")
	}
	scopes := p.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	audience := p.Audience
	if audience == "" {
		audience = DefaultAudience
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.ClientID},
		"redirect_uri":          {p.RedirectURI},
		"scope":                 {strings.Join(scopes, " ")},
		"audience":              {audience},
		"state":                 {p.State},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
	}
	return AuthURL + "?" + q.Encode(), nil
}

// Tokens is the OAuth token response (post-exchange or post-refresh).
// ExpiresAt is computed from issuance time + ExpiresIn so we don't have
// to keep re-doing the arithmetic.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Expired reports whether ExpiresAt has passed (with a small skew margin).
func (t *Tokens) Expired() bool {
	if t == nil || t.ExpiresAt.IsZero() {
		return true
	}
	return time.Until(t.ExpiresAt) < 60*time.Second
}

// ExchangeParams carry the code returned to the redirect URI plus the
// PKCE verifier we generated at the start of the flow.
type ExchangeParams struct {
	ClientID    string
	RedirectURI string
	Code        string
	Verifier    string
}

// Client wraps the small set of OAuth/cAPI calls we make. Construct one
// per running app; method calls are safe for concurrent use.
type Client struct {
	HTTPClient *http.Client
	// TokenEndpoint overrides TokenURL — useful for tests with httptest.
	TokenEndpoint string
	// DecodeEndpoint overrides DecodeURL.
	DecodeEndpoint string
	// Now is injected for tests; production leaves it nil and time.Now is used.
	Now func() time.Time
}

// NewClient builds a Client with a 30-second-timeout HTTP client.
func NewClient() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) tokenURL() string {
	if c.TokenEndpoint != "" {
		return c.TokenEndpoint
	}
	return TokenURL
}

// Exchange swaps an authorization code for access + refresh tokens.
func (c *Client) Exchange(ctx context.Context, p ExchangeParams) (*Tokens, error) {
	if p.ClientID == "" || p.Code == "" || p.Verifier == "" || p.RedirectURI == "" {
		return nil, errors.New("exchange: ClientID, Code, Verifier, RedirectURI all required")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {p.ClientID},
		"code":          {p.Code},
		"code_verifier": {p.Verifier},
		"redirect_uri":  {p.RedirectURI},
	}
	return c.postToken(ctx, form)
}

// Refresh trades a refresh token for a fresh access token. Frontier
// rotates refresh tokens, so the returned Tokens.RefreshToken should
// replace the previous one in persistent storage.
func (c *Client) Refresh(ctx context.Context, clientID, refreshToken string) (*Tokens, error) {
	if clientID == "" || refreshToken == "" {
		return nil, errors.New("refresh: clientID and refreshToken required")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
	}
	return c.postToken(ctx, form)
}

func (c *Client) postToken(ctx context.Context, form url.Values) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("frontier token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &TokenError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var tok Tokens
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("frontier token: decode: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("frontier token: empty access_token in response")
	}
	if tok.ExpiresIn > 0 {
		tok.ExpiresAt = c.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return &tok, nil
}

// TokenError is returned when the token endpoint replies non-2xx. The
// body usually contains a JSON {error, error_description} per RFC 6749.
type TokenError struct {
	StatusCode int
	Body       string
}

func (e *TokenError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return fmt.Sprintf("frontier token: HTTP %d: %s", e.StatusCode, body)
}

// Decoded is the parsed response from /decode — used to confirm the OAuth
// session's customer matches the commander we're tracking.
type Decoded struct {
	Usr struct {
		CustomerID json.Number `json:"customer_id"`
		Email      string      `json:"email"`
		Name       string      `json:"name"`
	} `json:"usr"`
}

// Decode calls /decode with a Bearer access token. Used to verify the
// token's owner.
func (c *Client) Decode(ctx context.Context, accessToken string) (*Decoded, error) {
	if accessToken == "" {
		return nil, errors.New("decode: accessToken required")
	}
	endpoint := c.DecodeEndpoint
	if endpoint == "" {
		endpoint = DecodeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("frontier decode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("frontier decode: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var d Decoded
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("frontier decode: %w", err)
	}
	return &d, nil
}
