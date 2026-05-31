// Package eddn uploads journal events and station-side JSON snapshots to the
// EDDN community data network (https://eddn.edcd.io). Covered schemas:
// journal/1 (FSDJump, Location, Docked, CarrierJump, Scan, SAASignalsFound),
// commodity/3, outfitting/2, shipyard/2, navroute/1, and the dedicated
// exploration schemas (fssdiscoveryscan, fssallbodiesfound, fsssignaldiscovered,
// fssbodysignals, navbeaconscan, scanbarycentre, approachsettlement, codexentry).
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
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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

// OutfittingReader / ShipyardReader / NavRouteReader load the matching
// sibling JSON file. Overridable so tests don't need real files on disk.
type OutfittingReader func(journalDir string) (*journal.OutfittingFile, error)
type ShipyardReader func(journalDir string) (*journal.ShipyardFile, error)
type NavRouteReader func(journalDir string) (*journal.NavRouteFile, error)

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
	// ReadOutfitting / ReadShipyard / ReadNavRoute override the matching
	// sibling-file loaders (tests). Production defaults read from disk.
	ReadOutfitting OutfittingReader
	ReadShipyard   ShipyardReader
	ReadNavRoute   NavRouteReader

	// sigBuf batches FSSSignalDiscovered events per system before upload —
	// see signals.go. Guarded by sigMu.
	sigMu  sync.Mutex
	sigBuf signalBuffer
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
	// Skip backfill events: replaying old jumps + docks to EDDN is
	// noise on the community feed even when it deduplicates upstream.
	if raw.Replayed {
		return nil
	}
	// Need at least a commander name for uploaderID; without it, the EDDN
	// header is invalid. Skip until LoadGame populates it.
	if u.Session.Commander() == "" {
		return nil
	}
	// EDDN's gateway drops messages tagged as Alpha/Beta gameversions, and
	// Legacy galaxy data belongs on a different schema/relay than the live
	// one we target. Filter both client-side so we don't waste EDDN's
	// validation cycles on data it'll reject anyway.
	if gv, _ := u.Session.GameVersion(); !isUploadableGameVersion(gv) {
		return nil
	}
	switch raw.Event {
	case journal.EventFSDJump, journal.EventCarrierJump, journal.EventLocation:
		// Leaving the previous system: post any signals we'd buffered there
		// before the journal/1 message updates our location.
		u.flushSignals(ctx)
		return u.uploadJournal(ctx, raw)
	case journal.EventDocked, journal.EventScan:
		return u.uploadJournal(ctx, raw)
	case journal.EventSAASignalsFound:
		// SAASignalsFound feeds two schemas: the full event on journal/1 and
		// its per-body signal counts on fssbodysignals/1 (matching EDMC).
		_ = u.uploadJournal(ctx, raw)
		return u.uploadFromBuilder(ctx, schemaFSSBodySignalsV1, buildFSSBodySignalsMessage, raw)
	case journal.EventMarket:
		return u.uploadMarket(ctx, raw)
	case journal.EventOutfitting:
		return u.uploadOutfitting(ctx, raw)
	case journal.EventShipyard:
		return u.uploadShipyard(ctx, raw)
	case journal.EventNavRoute:
		return u.uploadNavRoute(ctx, raw)
	case journal.EventFSSDiscoveryScan:
		u.flushSignals(ctx) // honk finished; ship the buffered signals
		return u.uploadScan(ctx, raw, schemaFSSDiscoveryScanV1, "SystemName",
			[]string{"BodyCount", "NonBodyCount"}, nil)
	case journal.EventFSSAllBodiesFound:
		return u.uploadScan(ctx, raw, schemaFSSAllBodiesFoundV1, "SystemName",
			[]string{"Count"}, nil)
	case journal.EventNavBeaconScan:
		return u.uploadScan(ctx, raw, schemaNavBeaconScanV1, "StarSystem",
			[]string{"NumBodies"}, nil)
	case journal.EventScanBaryCentre:
		return u.uploadScan(ctx, raw, schemaScanBaryCentreV1, "StarSystem",
			[]string{"BodyID"},
			[]string{"SemiMajorAxis", "Eccentricity", "OrbitalInclination",
				"Periapsis", "OrbitalPeriod", "AscendingNode", "MeanAnomaly"})
	case journal.EventApproachSettlement:
		return u.uploadFromBuilder(ctx, schemaApproachSettlementV1, buildApproachSettlementMessage, raw)
	case journal.EventCodexEntry:
		return u.uploadFromBuilder(ctx, schemaCodexEntryV1, buildCodexEntryMessage, raw)
	case journal.EventFSSBodySignals:
		return u.uploadFromBuilder(ctx, schemaFSSBodySignalsV1, buildFSSBodySignalsMessage, raw)
	case journal.EventFSSSignalDiscovered:
		return u.bufferFSSSignal(ctx, raw.Payload)
	}
	return nil
}

// builderFunc is the shape of the pure per-event message builders. A nil
// message means "nothing to upload" (e.g. an ApproachSettlement with no
// surface coordinates); errMissingStarPos means "skip until we know where
// we are".
type builderFunc func(payload []byte, sess *state.Session) (map[string]any, error)

// uploadFromBuilder runs a builder and posts the result, treating
// errMissingStarPos as a quiet skip and nil messages as no-ops.
func (u *Uploader) uploadFromBuilder(ctx context.Context, schemaRef string, build builderFunc, raw journal.Raw) error {
	msg, err := build(raw.Payload, u.Session)
	if err != nil {
		if errors.Is(err, errMissingStarPos) {
			return nil
		}
		u.status("WARN", fmt.Sprintf("EDDN: drop %s: %v", raw.Event, err))
		return nil
	}
	if msg == nil {
		return nil
	}
	return u.send(ctx, schemaRef, msg, raw.Event)
}

// uploadScan is uploadFromBuilder for the "system + scalar fields" schemas.
func (u *Uploader) uploadScan(ctx context.Context, raw journal.Raw, schemaRef, nameKey string, requiredScalars, optionalScalars []string) error {
	msg, err := buildScanMessage(raw.Payload, schemaRef, nameKey, requiredScalars, optionalScalars, u.Session)
	if err != nil {
		if errors.Is(err, errMissingStarPos) {
			return nil
		}
		u.status("WARN", fmt.Sprintf("EDDN: drop %s: %v", raw.Event, err))
		return nil
	}
	return u.send(ctx, schemaRef, msg, raw.Event)
}

// uploadOutfitting reads Outfitting.json and posts an outfitting/2 message.
func (u *Uploader) uploadOutfitting(ctx context.Context, raw journal.Raw) error {
	if u.JournalDir == "" {
		return nil
	}
	read := u.ReadOutfitting
	if read == nil {
		read = journal.ReadOutfittingFile
	}
	of, err := read(u.JournalDir)
	if err != nil {
		u.status("WARN", "EDDN: cannot read Outfitting.json: "+err.Error())
		return nil
	}
	if of.MarketID != marketIDOf(raw.Payload) {
		return nil // stale file; game hasn't flushed yet
	}
	msg := buildOutfittingMessage(of, u.Session)
	if msg == nil {
		return nil
	}
	label := fmt.Sprintf("outfitting for %s (%d modules)", of.StationName, len(of.Items))
	return u.send(ctx, schemaOutfittingV2, msg, label)
}

// uploadShipyard reads Shipyard.json and posts a shipyard/2 message.
func (u *Uploader) uploadShipyard(ctx context.Context, raw journal.Raw) error {
	if u.JournalDir == "" {
		return nil
	}
	read := u.ReadShipyard
	if read == nil {
		read = journal.ReadShipyardFile
	}
	sf, err := read(u.JournalDir)
	if err != nil {
		u.status("WARN", "EDDN: cannot read Shipyard.json: "+err.Error())
		return nil
	}
	if sf.MarketID != marketIDOf(raw.Payload) {
		return nil // stale file
	}
	msg := buildShipyardMessage(sf, u.Session)
	if msg == nil {
		return nil
	}
	label := fmt.Sprintf("shipyard for %s (%d ships)", sf.StationName, len(sf.PriceList))
	return u.send(ctx, schemaShipyardV2, msg, label)
}

// uploadNavRoute reads NavRoute.json and posts a navroute/1 message.
func (u *Uploader) uploadNavRoute(ctx context.Context, raw journal.Raw) error {
	if u.JournalDir == "" {
		return nil
	}
	read := u.ReadNavRoute
	if read == nil {
		read = journal.ReadNavRouteFile
	}
	nr, err := read(u.JournalDir)
	if err != nil {
		u.status("WARN", "EDDN: cannot read NavRoute.json: "+err.Error())
		return nil
	}
	msg := buildNavRouteMessage(nr, u.Session)
	if msg == nil {
		return nil
	}
	label := fmt.Sprintf("nav route (%d jumps)", len(nr.Route))
	return u.send(ctx, schemaNavRouteV1, msg, label)
}

// marketIDOf extracts the MarketID from a journal event payload, preserving
// precision. Returns 0 when absent.
func marketIDOf(payload []byte) int64 {
	ev, err := decodeEvent(payload)
	if err != nil {
		return 0
	}
	if n, ok := ev["MarketID"].(json.Number); ok {
		v, _ := n.Int64()
		return v
	}
	return 0
}

// isUploadableGameVersion returns false for the gameversion strings EDDN
// will not accept (alpha/beta) or that belong on a different relay
// (legacy). An empty string is treated as uploadable — we may not have
// seen LoadGame yet but the rest of the validation pipeline will catch
// any missing-required-field issues downstream.
func isUploadableGameVersion(v string) bool {
	v = strings.ToLower(v)
	if v == "" {
		return true
	}
	return !strings.Contains(v, "alpha") &&
		!strings.Contains(v, "beta") &&
		!strings.Contains(v, "legacy")
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
	return u.send(ctx, schemaJournalV1, msg, journalDescription(raw.Event, msg))
}

// journalDescription builds a human label for the status feed —
// "FSDJump → Sol" or "Docked at Abraham Lincoln (Sol)" — so the
// Activity tab tells the user WHAT was posted, not just the schema.
func journalDescription(eventName string, msg map[string]any) string {
	system, _ := msg["StarSystem"].(string)
	switch eventName {
	case journal.EventFSDJump, journal.EventCarrierJump:
		if system != "" {
			return fmt.Sprintf("%s → %s", eventName, system)
		}
	case journal.EventDocked:
		station, _ := msg["StationName"].(string)
		switch {
		case station != "" && system != "":
			return fmt.Sprintf("Docked at %s (%s)", station, system)
		case station != "":
			return fmt.Sprintf("Docked at %s", station)
		case system != "":
			return fmt.Sprintf("Docked in %s", system)
		}
	case journal.EventLocation:
		if system != "" {
			return fmt.Sprintf("Location: %s", system)
		}
	}
	return eventName
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
	commodities, _ := msg["commodities"].([]map[string]any)
	label := fmt.Sprintf("commodities for %s (%d items)", mf.StationName, len(commodities))
	return u.send(ctx, schemaCommodityV3, msg, label)
}

// send wraps the message in the EDDN envelope and POSTs it. label is a
// human description of what's being uploaded (e.g. "FSDJump → Sol")
// surfaced in the Activity tab on success.
//
// The body is gzip-compressed (Content-Encoding: gzip) per EDDN's
// recommendation — these JSON envelopes compress 3-5× and EDDN's
// gateway processes a high volume of uploads, so respecting the
// gzip recommendation is the polite default.
func (u *Uploader) send(ctx context.Context, schemaRef string, message map[string]any, label string) error {
	gv, gb := u.Session.GameVersion()
	if u.TestMode {
		schemaRef += "/test"
	}
	header := map[string]any{
		"uploaderID":      u.Session.Commander(),
		"softwareName":    u.Software.Name,
		"softwareVersion": u.Software.Version,
	}
	// Omit gameversion/gamebuild from the header when unknown rather than
	// sending empty strings — the schema allows the keys to be absent and
	// some downstream listeners reject zero-length values.
	if gv != "" {
		header["gameversion"] = gv
	}
	if gb != "" {
		header["gamebuild"] = gb
	}
	envelope := map[string]any{
		"$schemaRef": schemaRef,
		"header":     header,
		"message":    message,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(body); err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Endpoint, &compressed)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
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
	if label != "" {
		u.status("OK", fmt.Sprintf("EDDN: posted %s — %s", shortSchema(schemaRef), label))
	} else {
		u.status("OK", fmt.Sprintf("EDDN: posted %s", shortSchema(schemaRef)))
	}
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
