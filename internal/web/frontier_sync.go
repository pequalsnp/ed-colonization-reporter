package web

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/frontier"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// frontierPollInterval is our outer loop cadence. cAPI enforces a 15-min
// server-side cooldown on /fleetcarrier; we tick at the same rhythm and
// rely on the cAPI client's internal rate-limit check to deduplicate
// trigger-driven kicks.
const frontierPollInterval = 15 * time.Minute

// runFrontierCAPISync periodically polls cAPI for the authoritative
// Fleet Carrier inventory and forwards it to ravencolonial. Runs
// continuously until ctx is cancelled; cheap to leave running when the
// user hasn't signed in (the poll function early-returns).
//
// The very first sync is deferred until the tailer signals live mode,
// because the cAPI snapshot is anchored to the journal's most recent
// CarrierStats event — and that anchor is only reliably the latest
// once backfill replay has finished. After that first sync, the queued
// backfill FC deltas are flushed onto the new baseline (timestamp-
// gated, so anything already in cAPI's snapshot is skipped).
func (s *Server) runFrontierCAPISync(ctx context.Context) {
	t := time.NewTicker(frontierPollInterval)
	defer t.Stop()

	// Wait for backfill to complete (or fire immediately if StartAtEnd).
	select {
	case <-ctx.Done():
		return
	case <-s.liveModeCh:
	}

	s.pollFleetCarrierIfEnabled(ctx)
	if s.rep != nil {
		s.rep.FlushPendingFCDeltas()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollFleetCarrierIfEnabled(ctx)
		case <-s.frontierTrigger:
			s.pollFleetCarrierIfEnabled(ctx)
		}
	}
}

func (s *Server) pollFleetCarrierIfEnabled(ctx context.Context) {
	s.mu.Lock()
	enabled := s.cfg.FrontierCAPIEnabled
	s.mu.Unlock()
	if !enabled {
		return
	}
	if !s.frontierCAPI.HasTokens(ctx) {
		return
	}
	s.pollFleetCarrier(ctx)
}

// pollFleetCarrier does one cAPI fetch and forwards the result. Exposed
// (lowercase only within the package) for sign-in to call directly.
func (s *Server) pollFleetCarrier(ctx context.Context) {
	fc, err := s.frontierCAPI.FleetCarrier(ctx)
	if err != nil {
		if errors.Is(err, frontier.ErrFleetCarrierRateLimited) {
			// Expected when the user clicks "Sign in" twice within 15 min,
			// or when ticker + trigger fire close together.
			return
		}
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "cAPI /fleetcarrier failed: " + err.Error(),
		})
		return
	}
	if fc.MarketID == 0 {
		// Could mean the commander really has no FC, or that Frontier
		// shipped a payload shape we don't recognise. Dump the response
		// so an operator can paste it back; preview a tiny snippet in
		// the activity log so the symptom is at least visible.
		dumpPath := filepath.Join(filepath.Dir(resolveFrontierTokenPath()), "fleetcarrier_debug.json")
		hint := ""
		if err := fc.DumpResponse(dumpPath); err == nil {
			hint = " (raw response dumped to " + dumpPath + ")"
		}
		preview := ""
		if len(fc.RawBody) > 0 {
			n := 120
			if n > len(fc.RawBody) {
				n = len(fc.RawBody)
			}
			preview = " — first " + strconv.Itoa(n) + " bytes: " + string(fc.RawBody[:n])
		}
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelWarn,
			Message: "cAPI returned a FleetCarrier with no recognised MarketID field" + hint + preview,
		})
		return
	}

	// Register/update the owned carrier so other code paths (Market event,
	// CargoTransfer, etc.) recognise it.
	name := fc.Name.Filtered
	s.session.RegisterOwnedCarrier(state.OwnedCarrier{
		MarketID:   fc.MarketID,
		Name:       name,
		Callsign:   fc.Name.Callsign,
		StarSystem: fc.CurrentStarSystem,
	})

	cargo := ravencolonial.Cargo{}
	for _, item := range fc.Cargo {
		key := frontier.CommodityKey(item.Commodity)
		if key == "" || item.Quantity == 0 {
			continue
		}
		cargo[key] += item.Quantity
	}

	// Cache the snapshot so the GUI can render it.
	//
	// Important: we used to also POST OverwriteCarrierCargo to
	// ravencolonial on every cAPI sync, but cAPI is observed to lag the
	// game's CarrierStats event by 30+ minutes, so the overwrite
	// repeatedly clobbered ravencolonial's accumulated PATCH-delta state
	// with stale numbers. Now the snapshot only updates the local cache;
	// ravencolonial is kept in sync by the PATCH-delta path on each
	// CargoTransfer / MarketBuy / MarketSell event, which is live.
	//
	// (A user-triggered "force overwrite" path is in the works for
	// reconciling RC when local + RC drift apart; for now, RC heals via
	// deltas alone.)
	s.SetFCInventory(fc.Name.Filtered, cargo)

	total := 0
	for _, n := range cargo {
		total += n
	}
	s.hub.Publish(reporter.Status{
		Time: time.Now(), Level: reporter.LevelOK,
		Message: fmt.Sprintf("Synced FC %s from cAPI (%d distinct commodities, %d units) — local only, RC not touched", fc.Name.Callsign, len(cargo), total),
	})
}
