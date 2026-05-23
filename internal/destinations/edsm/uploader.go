// Package edsm uploads journal events to EDSM (https://www.edsm.net), the
// community star-system database. EDSM's `api-journal-v1` endpoint accepts
// the player's journal events more or less verbatim with a few
// EDMC-conventional "transient" fields added (_systemName, _systemCoordinates,
// _stationName, _shipId) so that events like Scan can be correlated to a
// system even though the journal entry itself doesn't name one.
//
// This implementation is independent of EDMC's edsm.py (GPLv2); EDMC was
// consulted for protocol shape but no code was copied.
package edsm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// DefaultEndpoint is the EDSM journal upload URL.
const DefaultEndpoint = "https://www.edsm.net/api-journal-v1"

// SoftwareID identifies the uploader in EDSM's analytics. EDMC sends
// `EDMarketConnector` and a version string — we send our own equivalents.
type SoftwareID struct {
	Name    string
	Version string
}

// Uploader is the EDSM destination.
type Uploader struct {
	Endpoint   string
	Software   SoftwareID
	Session    *state.Session
	HTTPClient *http.Client

	mu      sync.RWMutex
	apiKey  string
	enabled atomic.Bool

	discard *discardSet

	// nextAllowed is the time after which we may issue the next request,
	// per the X-Rate-Limit-Reset header EDSM may return when we hit a cap.
	nextAllowed atomic.Int64 // Unix seconds

	OnStatus func(level, msg string)
}

// New builds an EDSM uploader. The discard list is not fetched here; call
// StartBackground after the server is up.
func New(software SoftwareID, sess *state.Session) *Uploader {
	return &Uploader{
		Endpoint:   DefaultEndpoint,
		Software:   software,
		Session:    sess,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		discard:    newDiscardSet(),
	}
}

// Name implements destinations.Destination.
func (u *Uploader) Name() string { return "EDSM" }

// SetEnabled toggles uploads at runtime.
func (u *Uploader) SetEnabled(b bool) { u.enabled.Store(b) }

// Enabled reports the current enable state.
func (u *Uploader) Enabled() bool { return u.enabled.Load() }

// SetAPIKey updates the EDSM API key.
func (u *Uploader) SetAPIKey(k string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.apiKey = k
}

// apiKeyCopy returns the current API key. Held in a method so callers don't
// touch the mutex directly.
func (u *Uploader) apiKeyCopy() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.apiKey
}

// StartBackground fires off the periodic discard-list refresher. Returns
// immediately; the refresh runs until ctx is cancelled.
func (u *Uploader) StartBackground(ctx context.Context) {
	go u.discard.RefreshLoop(ctx, u.HTTPClient, 24*time.Hour, func(err error) {
		u.status("WARN", "EDSM discard list refresh failed: "+err.Error())
	})
}

// HandleEvent implements destinations.Destination.
func (u *Uploader) HandleEvent(ctx context.Context, raw journal.Raw) error {
	if !u.enabled.Load() {
		return destinations.ErrDisabled
	}
	// Skip backfill: EDSM's API would happily accept old events with
	// their original timestamps, but the user opted into the relay to
	// share live activity, not to bulk-import a single replayed session.
	if raw.Replayed {
		return nil
	}
	key := u.apiKeyCopy()
	if key == "" {
		return nil // no key configured; silently skip
	}
	cmdr := u.Session.Commander()
	if cmdr == "" {
		return nil
	}
	if u.discard.Has(raw.Event) {
		return nil
	}
	// Respect rate-limit backoff if the last response told us to slow down.
	if next := u.nextAllowed.Load(); next > 0 && time.Now().Unix() < next {
		return nil
	}

	// Decode the event into a map so we can inject transient hints.
	var event map[string]any
	if err := json.Unmarshal(raw.Payload, &event); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	u.attachTransients(event)

	return u.post(ctx, cmdr, key, []map[string]any{event})
}

// attachTransients adds the underscore-prefixed context fields EDSM expects.
// Source-of-truth for the player's current system, coords, and dock state
// is the shared Session; for events that already carry these fields they
// stay (EDSM ignores duplicates).
func (u *Uploader) attachTransients(event map[string]any) {
	sysName, _ := u.Session.System()
	if sysName != "" {
		event["_systemName"] = sysName
	}
	if pos, ok := u.Session.StarPos(); ok {
		event["_systemCoordinates"] = []float64{pos[0], pos[1], pos[2]}
	}
	if docked, station, _ := u.Session.Dock(); docked && station != "" {
		event["_stationName"] = station
	}
}

// post sends a batch (single event today) and processes the response.
func (u *Uploader) post(ctx context.Context, cmdr, key string, events []map[string]any) error {
	msg, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}
	gv, gb := u.Session.GameVersion()
	form := url.Values{
		"commanderName":       {cmdr},
		"apiKey":              {key},
		"fromSoftware":        {u.Software.Name},
		"fromSoftwareVersion": {u.Software.Version},
		"fromGameVersion":     {gv},
		"fromGameBuild":       {gb},
		"message":             {string(msg)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		u.status("ERROR", "EDSM upload failed: "+err.Error())
		return err
	}
	defer resp.Body.Close()

	// Update rate-limit window if the server told us.
	if remaining, _ := strconv.Atoi(resp.Header.Get("X-Rate-Limit-Remaining")); remaining == 0 {
		if reset, err := strconv.ParseInt(resp.Header.Get("X-Rate-Limit-Reset"), 10, 64); err == nil && reset > 0 {
			u.nextAllowed.Store(reset)
			u.status("WARN", fmt.Sprintf("EDSM rate-limited; pausing until %s", time.Unix(reset, 0).Format(time.RFC3339)))
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		err := fmt.Errorf("EDSM: %s — %s", resp.Status, string(snippet))
		u.status("ERROR", err.Error())
		return err
	}

	// EDSM puts its own success/error in the body as msgnum (1xx/2xx/4xx/5xx).
	var reply edsmReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		u.status("WARN", "EDSM: cannot decode reply: "+err.Error())
		return nil // not fatal — the upload may still have landed
	}
	switch {
	case reply.MsgNum/100 == 1:
		u.status("OK", fmt.Sprintf("EDSM: %d event accepted", len(events)))
	case reply.MsgNum/100 == 5:
		// "Server saved for later" — count as success.
		u.status("INFO", fmt.Sprintf("EDSM: deferred (msg %d): %s", reply.MsgNum, reply.Msg))
	case reply.MsgNum/100 == 2:
		u.status("ERROR", fmt.Sprintf("EDSM rejected upload (msg %d): %s", reply.MsgNum, reply.Msg))
	case reply.MsgNum/100 == 4:
		u.status("WARN", fmt.Sprintf("EDSM ignored event (msg %d): %s", reply.MsgNum, reply.Msg))
	default:
		u.status("INFO", fmt.Sprintf("EDSM msg %d: %s", reply.MsgNum, reply.Msg))
	}
	return nil
}

func (u *Uploader) status(level, msg string) {
	if u.OnStatus != nil {
		u.OnStatus(level, msg)
	}
}

// edsmReply is the documented top-level EDSM response shape.
type edsmReply struct {
	MsgNum int    `json:"msgnum"`
	Msg    string `json:"msg"`
}
