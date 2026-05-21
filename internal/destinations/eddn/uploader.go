// Package eddn uploads journal events and Market.json snapshots to the EDDN
// community data network (https://eddn.edcd.io).
//
// EDDN ingests anonymous, schema-validated journal data from many uploaders
// and rebroadcasts it for downstream consumers (Inara, EDSM, third-party
// trading sites, etc.). No API key is required, but the upload format is
// strict — see internal/destinations/eddn/strip.go for the field stripping
// rules and journal.go/commodity.go for the per-schema transformations.
//
// This implementation is independent of EDMC's eddn.py (GPLv2); EDMC was
// consulted as a reference for which events to relay and which fields to
// strip, but no code was copied.
package eddn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// DefaultEndpoint is the EDDN live upload URL, including the non-standard port.
const DefaultEndpoint = "https://eddn.edcd.io:4430/upload/"

// BetaEndpoint is the EDDN beta gateway, used together with TestMode for
// integration testing. Messages posted here do not reach live subscribers.
const BetaEndpoint = "https://beta.eddn.edcd.io:4431/upload/"

// SoftwareID identifies our uploader in the EDDN header so EDDN operators
// can correlate bad uploads back to the source software.
type SoftwareID struct {
	Name    string
	Version string
}

// MarketReader loads Market.json given a journal directory. The Uploader
// uses this on the Market event to fetch the commodity inventory.
type MarketReader func(journalDir string) (*journal.MarketFile, error)

// Uploader is the EDDN destination. Pointer methods are safe for concurrent
// calls.
type Uploader struct {
	Endpoint   string
	Software   SoftwareID
	Session    *state.Session
	JournalDir string
	// HTTPClient defaults to a 20s-timeout client. Tests inject their own.
	HTTPClient *http.Client
	// ReadMarket overrides the Market.json loader (tests).
	ReadMarket MarketReader
	// TestMode appends `/test` to every schemaRef so EDDN's gateway runs
	// validation without broadcasting to live consumers. Combine with
	// `Endpoint = BetaEndpoint` for full beta-network integration tests.
	TestMode bool

	enabled atomic.Bool
	// OnStatus, if set, receives user-visible status updates (success/skip/error).
	OnStatus func(level string, msg string)
}

// New builds an Uploader with default endpoint and HTTP client. Pass the
// shared Session so it can read commander, system, gameversion, and DLC
// flags as state advances.
func New(software SoftwareID, sess *state.Session) *Uploader {
	return &Uploader{
		Endpoint:   DefaultEndpoint,
		Software:   software,
		Session:    sess,
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// Name implements destinations.Destination.
func (u *Uploader) Name() string { return "EDDN" }

// SetEnabled turns uploads on or off without rebuilding the Uploader.
func (u *Uploader) SetEnabled(b bool) { u.enabled.Store(b) }

// Enabled reports whether uploads are currently turned on.
func (u *Uploader) Enabled() bool { return u.enabled.Load() }

// HandleEvent inspects the journal event and uploads if it's one EDDN
// accepts. Returns destinations.ErrDisabled when the uploader is off so
// the multiplexer can quiet the corresponding status path.
func (u *Uploader) HandleEvent(ctx context.Context, raw journal.Raw) error {
	if !u.enabled.Load() {
		return destinations.ErrDisabled
	}
	// Need at least a commander name for uploaderID; without it, the EDDN
	// header is invalid. Skip until LoadGame populates it.
	if u.Session.Commander() == "" {
		return nil
	}
	switch raw.Event {
	case journal.EventFSDJump, journal.EventLocation, journal.EventDocked, journal.EventCarrierJump:
		return u.uploadJournal(ctx, raw)
	case journal.EventMarket:
		return u.uploadMarket(ctx, raw)
	}
	return nil
}

func (u *Uploader) uploadJournal(ctx context.Context, raw journal.Raw) error {
	msg, err := buildJournalMessage(raw, u.Session)
	if err != nil {
		if errors.Is(err, errMissingStarPos) {
			// Quietly skip; this is expected on the very first Docked of a
			// session before we've seen Location/FSDJump.
			return nil
		}
		u.status("WARN", fmt.Sprintf("EDDN: drop %s: %v", raw.Event, err))
		return nil
	}
	if msg == nil {
		return nil
	}
	return u.send(ctx, schemaJournalV1, msg)
}

func (u *Uploader) uploadMarket(ctx context.Context, raw journal.Raw) error {
	var ev journal.MarketEvent
	if err := json.Unmarshal(raw.Payload, &ev); err != nil {
		return fmt.Errorf("market event: %w", err)
	}
	if u.JournalDir == "" {
		return nil // can't read Market.json without a dir
	}
	read := u.ReadMarket
	if read == nil {
		read = journal.ReadMarketFile
	}
	mf, err := read(u.JournalDir)
	if err != nil {
		u.status("WARN", "EDDN: cannot read Market.json: "+err.Error())
		return nil
	}
	if mf.MarketID != ev.MarketID {
		// Market.json is stale; the game hasn't flushed yet. Skip rather
		// than upload data for the wrong station.
		return nil
	}
	msg := buildCommodityMessage(mf, u.Session)
	if msg == nil {
		return nil // empty or all-filtered commodity list
	}
	return u.send(ctx, schemaCommodityV3, msg)
}

// send wraps the message in the EDDN envelope and POSTs it.
func (u *Uploader) send(ctx context.Context, schemaRef string, message map[string]any) error {
	gv, gb := u.Session.GameVersion()
	if u.TestMode {
		schemaRef += "/test"
	}
	envelope := map[string]any{
		"$schemaRef": schemaRef,
		"header": map[string]any{
			"uploaderID":      u.Session.Commander(),
			"softwareName":    u.Software.Name,
			"softwareVersion": u.Software.Version,
			"gameversion":     gv,
			"gamebuild":       gb,
		},
		"message": message,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		u.status("ERROR", "EDDN upload failed: "+err.Error())
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		err := fmt.Errorf("EDDN %s: %s — %s", schemaRef, resp.Status, string(snippet))
		u.status("ERROR", err.Error())
		return err
	}
	u.status("OK", fmt.Sprintf("EDDN: posted %s", shortSchema(schemaRef)))
	return nil
}

func (u *Uploader) status(level, msg string) {
	if u.OnStatus != nil {
		u.OnStatus(level, msg)
	}
}

func shortSchema(s string) string {
	const prefix = "https://eddn.edcd.io/schemas/"
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
