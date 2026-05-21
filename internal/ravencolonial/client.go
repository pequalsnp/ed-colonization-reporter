package ravencolonial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the canonical Azure host for the ravencolonial API. The
// public ravencolonial.space domain 301s here; we point at the canonical host
// directly so a momentary DNS redirect outage doesn't break us.
const DefaultBaseURL = "https://ravencolonial100-awcbdvabgze4c5cq.canadacentral-01.azurewebsites.net"

const defaultTimeout = 20 * time.Second

// Client is a minimal HTTP client for the ravencolonial colonization endpoints
// the reporter calls. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the default base URL. Trailing slashes are trimmed.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithAPIKey sets the rcc-key header for write operations that require auth
// (Fleet Carrier and system-site writes). The colonization-reporting flow
// does not require a key.
func WithAPIKey(k string) Option {
	return func(c *Client) { c.apiKey = k }
}

// WithHTTPClient overrides the underlying http.Client. Useful for tests.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.hc = hc }
}

// New builds a Client. The default HTTP client has a 20s timeout; override
// with WithHTTPClient if you need different behaviour.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		hc:      &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is returned for non-2xx responses. It carries the HTTP status and
// a snippet of the response body for diagnostics.
type APIError struct {
	StatusCode int
	Status     string
	Body       string
	URL        string
}

func (e *APIError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return fmt.Sprintf("ravencolonial: %s %s: %s", e.URL, e.Status, body)
}

// IsNotFound reports whether err is an APIError with status 404. Callers use
// this to distinguish "no project for this market yet" from real failures.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// ProjectBySystemMarket fetches the project (if any) associated with a given
// star system id64 and market ID. Returns a wrapped APIError; check
// IsNotFound to distinguish "no project at this market" from real failures.
func (c *Client) ProjectBySystemMarket(ctx context.Context, systemAddress, marketID int64) (*Project, error) {
	path := fmt.Sprintf("/api/system/%s/%s",
		strconv.FormatInt(systemAddress, 10),
		strconv.FormatInt(marketID, 10))
	var p Project
	if err := c.do(ctx, http.MethodGet, path, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateProject posts an updated commodity-needs snapshot for a project,
// derived from a ColonisationConstructionDepot event.
func (c *Client) UpdateProject(ctx context.Context, update ProjectUpdate) error {
	if update.BuildID == "" {
		return errors.New("UpdateProject: BuildID required")
	}
	path := "/api/project/" + url.PathEscape(update.BuildID)
	return c.do(ctx, http.MethodPost, path, update, nil)
}

// CompleteProject marks a build as complete.
func (c *Client) CompleteProject(ctx context.Context, buildID string) error {
	if buildID == "" {
		return errors.New("CompleteProject: buildID required")
	}
	path := "/api/project/" + url.PathEscape(buildID) + "/complete"
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

// Contribute attributes a commander's contribution to a project. The cmdr
// argument is the commander name string from the Commander event.
func (c *Client) Contribute(ctx context.Context, buildID, cmdr string, contrib Contribution) error {
	if buildID == "" || cmdr == "" {
		return errors.New("Contribute: buildID and cmdr required")
	}
	if len(contrib) == 0 {
		return nil // nothing to send
	}
	path := "/api/project/" + url.PathEscape(buildID) + "/contribute/" + url.PathEscape(cmdr)
	return c.do(ctx, http.MethodPost, path, contrib, nil)
}

// PutFleetCarrier registers or updates the Fleet Carrier metadata for a
// MarketID. Requires the rcc-key (configured via WithAPIKey); the server
// will return 401/403 if no key is set.
func (c *Client) PutFleetCarrier(ctx context.Context, fc FleetCarrier) error {
	if fc.MarketID == 0 {
		return errors.New("PutFleetCarrier: MarketID required")
	}
	if c.apiKey == "" {
		return ErrNoAPIKey
	}
	path := fmt.Sprintf("/api/fc/%d", fc.MarketID)
	return c.do(ctx, http.MethodPut, path, fc, nil)
}

// OverwriteCarrierCargo replaces the cargo snapshot stored for a Fleet
// Carrier on the server. Pass the {commodity: stock} map you parsed from
// Market.json. Requires the rcc-key.
func (c *Client) OverwriteCarrierCargo(ctx context.Context, marketID int64, cargo Cargo) error {
	if marketID == 0 {
		return errors.New("OverwriteCarrierCargo: marketID required")
	}
	if c.apiKey == "" {
		return ErrNoAPIKey
	}
	// nil-safe: send an empty object for empty cargo so the server clears it.
	if cargo == nil {
		cargo = Cargo{}
	}
	path := fmt.Sprintf("/api/fc/%d/cargo", marketID)
	return c.do(ctx, http.MethodPost, path, cargo, nil)
}

// ErrNoAPIKey is returned by FC-write methods when no rcc-key is configured.
// Callers can match on this to silently skip Fleet Carrier sync rather than
// surfacing a server-side 401 to the user.
var ErrNoAPIKey = errors.New("ravencolonial: API key (rcc-key) required for this endpoint")

// ActiveProjects lists the projects a commander is linked to.
func (c *Client) ActiveProjects(ctx context.Context, cmdr string) ([]Project, error) {
	if cmdr == "" {
		return nil, errors.New("ActiveProjects: cmdr required")
	}
	path := "/api/cmdr/" + url.PathEscape(cmdr) + "/active"
	var out []Project
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// do performs an HTTP request and decodes the JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("rcc-key", c.apiKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, fullURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return &APIError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(respBody),
			URL:        fullURL,
		}
	}
	if out == nil {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s: %w", fullURL, err)
	}
	return nil
}
