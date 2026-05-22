package web

import (
	"context"
	"errors"
	"fmt"
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
func (s *Server) runFrontierCAPISync(ctx context.Context) {
	t := time.NewTicker(frontierPollInterval)
	defer t.Stop()
	// Kick once after startup so a previously-signed-in user gets an
	// immediate sync without waiting 15 minutes.
	s.kickFrontierSync()
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
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelInfo,
			Message: "cAPI: commander has no Fleet Carrier",
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

	if err := s.client.OverwriteCarrierCargo(ctx, fc.MarketID, cargo); err != nil {
		if errors.Is(err, ravencolonial.ErrNoAPIKey) {
			s.hub.Publish(reporter.Status{
				Time: time.Now(), Level: reporter.LevelWarn,
				Message: "cAPI fetched FC inventory but no rcc-key set; can't POST to ravencolonial",
			})
			return
		}
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "Sync FC cargo (from cAPI) failed: " + err.Error(),
		})
		return
	}

	total := 0
	for _, n := range cargo {
		total += n
	}
	s.hub.Publish(reporter.Status{
		Time: time.Now(), Level: reporter.LevelOK,
		Message: fmt.Sprintf("Synced FC %s from cAPI (%d distinct commodities, %d units)", fc.Name.Callsign, len(cargo), total),
	})
}
