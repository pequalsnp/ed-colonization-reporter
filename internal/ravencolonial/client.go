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
	// Truncate at 1 KiB — enough to see ASP.NET ProblemDetails responses
	// (which include the failing model name + field path) without
	// flooding the activity log on huge HTML error pages.
	if len(body) > 1024 {
		body = body[:1024] + "…"
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

// CreateProject registers a new build with ravencolonial. Returns the
// full Project — most importantly the server-assigned BuildID, which
// callers must cache for subsequent UpdateProject / Contribute calls.
func (c *Client) CreateProject(ctx context.Context, p ProjectCreate) (*Project, error) {
	if p.MarketID == 0 {
		return nil, errors.New("CreateProject: MarketID required")
	}
	if p.SystemAddress == 0 {
		return nil, errors.New("CreateProject: SystemAddress required")
	}
	if p.SystemName == "" {
		return nil, errors.New("CreateProject: SystemName required")
	}
	if p.Commodities == nil {
		// Server rejects null but accepts {}; normalise here so callers
		// don't have to remember.
		p.Commodities = map[string]int{}
	}
	var out Project
	if err := c.do(ctx, http.MethodPut, "/api/project/", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// SetSystemArchitect records a commander as the architect of a system.
// Path is /api/v2/system/{name}/sites with body {"architect": cmdr}.
// Requires rcc-key per SrvSurvey (RavenColonial.cs:367-390).
func (c *Client) SetSystemArchitect(ctx context.Context, systemName, cmdr string) error {
	if systemName == "" || cmdr == "" {
		return errors.New("SetSystemArchitect: systemName and cmdr required")
	}
	if c.apiKey == "" {
		return ErrNoAPIKey
	}
	path := "/api/v2/system/" + url.PathEscape(systemName) + "/sites"
	body := map[string]string{"architect": cmdr}
	return c.do(ctx, http.MethodPut, path, body, nil)
}

// LinkedCarriers fetches the FCs the commander has linked to projects
// via the ravencolonial website (independent of in-game CarrierStats
// events). Used at startup so we know which markets to track without
// waiting for the game to emit a fresh CarrierStats.
type LinkedCarrier struct {
	MarketID   int64  `json:"marketId"`
	Name       string `json:"name"`
	Callsign   string `json:"callsign"`
	StarSystem string `json:"starSystem"`
}

// CommanderCarriers returns the commander's linked FCs.
func (c *Client) CommanderCarriers(ctx context.Context, cmdr string) ([]LinkedCarrier, error) {
	if cmdr == "" {
		return nil, errors.New("CommanderCarriers: cmdr required")
	}
	path := "/api/cmdr/" + url.PathEscape(cmdr) + "/fc/all"
	var out []LinkedCarrier
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PatchProject applies a sparse update to a project — used for tagging
// factionName and bodyName/bodyNum once the commander docks at a build
// site that didn't have those fields populated when it was created.
type ProjectPatch struct {
	FactionName string  `json:"factionName,omitempty"`
	BodyName    string  `json:"bodyName,omitempty"`
	BodyNum     *int    `json:"bodyNum,omitempty"`
}

// PatchProject POSTs a sparse partial-update to /api/project/{buildId}.
// SrvSurvey uses the same endpoint for both full updates and these
// metadata patches (ColonyData.cs:283-327).
func (c *Client) PatchProject(ctx context.Context, buildID string, patch ProjectPatch) error {
	if buildID == "" {
		return errors.New("PatchProject: buildID required")
	}
	if patch.FactionName == "" && patch.BodyName == "" && patch.BodyNum == nil {
		return nil // nothing to send
	}
	path := "/api/project/" + url.PathEscape(buildID)
	return c.do(ctx, http.MethodPost, path, patch, nil)
}

// PatchCarrierCargo applies a delta to the FC's stored cargo. Positive
// values add, negative values remove. Used when the player transfers
// cargo to/from the carrier mid-session — there is no full inventory
// snapshot available outside of the commodities-market UI, so we
// maintain server-side state by accumulating deltas.
func (c *Client) PatchCarrierCargo(ctx context.Context, marketID int64, delta Cargo) error {
	if marketID == 0 {
		return errors.New("PatchCarrierCargo: marketID required")
	}
	if c.apiKey == "" {
		return ErrNoAPIKey
	}
	if len(delta) == 0 {
		return nil
	}
	path := fmt.Sprintf("/api/fc/%d/cargo", marketID)
	return c.do(ctx, http.MethodPatch, path, delta, nil)
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
