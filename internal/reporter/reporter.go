// Package reporter wires journal events to ravencolonial API calls.
//
// The reporter owns the policy that turns "the player just docked at a
// construction site" or "the player just delivered titanium" into the right
// HTTP calls, with deduplication against the session cache so we don't
// hammer the API on every keystroke.
package reporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// APIClient is the subset of ravencolonial.Client the reporter uses. Defining
// it as an interface lets tests substitute a fake.
type APIClient interface {
	ProjectBySystemMarket(ctx context.Context, systemAddress, marketID int64) (*ravencolonial.Project, error)
	UpdateProject(ctx context.Context, update ravencolonial.ProjectUpdate) error
	CompleteProject(ctx context.Context, buildID string) error
	Contribute(ctx context.Context, buildID, cmdr string, contrib ravencolonial.Contribution) error
	PutFleetCarrier(ctx context.Context, fc ravencolonial.FleetCarrier) error
	OverwriteCarrierCargo(ctx context.Context, marketID int64, cargo ravencolonial.Cargo) error
}

// Status is a user-visible status update emitted by the reporter.
type Status struct {
	Time    time.Time
	Level   Level
	Message string
}

// Level classifies status messages.
type Level int

const (
	LevelInfo Level = iota
	LevelOK
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelOK:
		return "OK"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Reporter consumes parsed journal events and dispatches API calls.
//
// HandleEvent is the entry point — typically called in a loop fed by the
// journal tailer. It is safe to call HandleEvent concurrently with reads of
// the Session.
type Reporter struct {
	API     APIClient
	Session *state.Session
	// JournalDir lets the reporter read sibling files like Market.json on
	// EventMarket. May be empty, in which case FC cargo sync is skipped.
	JournalDir string
	// Now is injected for tests; production leaves it nil and time.Now is used.
	Now func() time.Time
	// onStatus, if set, receives every status update. The UI subscribes here.
	onStatus func(Status)

	// readMarketFile is overridable for tests. Production leaves it nil and
	// the package-default implementation reads from JournalDir.
	readMarketFile func(dir string) (*journal.MarketFile, error)
}

// New constructs a Reporter.
func New(api APIClient, sess *state.Session) *Reporter {
	return &Reporter{API: api, Session: sess}
}

// Name implements the destinations.Destination interface.
func (r *Reporter) Name() string { return "ravencolonial" }

// OnStatus registers a callback for status updates. Passing nil clears it.
// The callback is invoked synchronously; long-running consumers should
// hand off to a goroutine.
func (r *Reporter) OnStatus(fn func(Status)) {
	r.onStatus = fn
}

func (r *Reporter) emit(level Level, format string, args ...any) {
	if r.onStatus == nil {
		return
	}
	ts := time.Now()
	if r.Now != nil {
		ts = r.Now()
	}
	r.onStatus(Status{Time: ts, Level: level, Message: fmt.Sprintf(format, args...)})
}

// HandleEvent dispatches a single parsed journal line. Unknown events are
// silently ignored.
func (r *Reporter) HandleEvent(ctx context.Context, raw journal.Raw) error {
	switch raw.Event {
	case journal.EventFileheader:
		var e journal.FileheaderEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("fileheader: %w", err)
		}
		r.Session.SetGameVersion(e.GameVersion, e.GameBuild)
	case journal.EventCommander:
		var e journal.CommanderEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("commander: %w", err)
		}
		r.Session.SetCommander(e.Name, e.FID)
		r.emit(LevelInfo, "Commander: %s", e.Name)
	case journal.EventLoadGame:
		var e journal.LoadGameEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("loadgame: %w", err)
		}
		if e.Commander != "" {
			r.Session.SetCommander(e.Commander, e.FID)
		}
		if e.GameVersion != "" || e.GameBuild != "" {
			r.Session.SetGameVersion(e.GameVersion, e.GameBuild)
		}
		// LoadGame may omit Horizons/Odyssey on older clients; pointer
		// preserves the distinction between "absent" and "explicitly false".
		r.Session.SetDLCFlags(e.Horizons, e.Odyssey)
	case journal.EventLocation, journal.EventFSDJump:
		var e journal.LocationLikeEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("%s: %w", raw.Event, err)
		}
		r.Session.SetSystemWithPos(e.StarSystem, e.SystemAddress, e.StarPos)
	case journal.EventDocked:
		var e journal.DockedEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("docked: %w", err)
		}
		if e.StarSystem != "" && e.SystemAddress != 0 {
			r.Session.SetSystem(e.StarSystem, e.SystemAddress)
		}
		r.Session.SetDocked(e.StationName, e.MarketID, e.SystemAddress)
	case journal.EventUndocked:
		r.Session.SetUndocked()
	case journal.EventColonisationConstructionDepot:
		var e journal.ColonisationConstructionDepotEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("depot: %w", err)
		}
		return r.handleDepot(ctx, e)
	case journal.EventColonisationContribution:
		var e journal.ColonisationContributionEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("contribution: %w", err)
		}
		return r.handleContribution(ctx, e)
	case journal.EventCarrierStats:
		var e journal.CarrierStatsEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("carrierstats: %w", err)
		}
		return r.handleCarrierStats(ctx, e)
	case journal.EventCarrierLocation:
		var e journal.CarrierLocationEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("carrierlocation: %w", err)
		}
		r.Session.RegisterOwnedCarrier(state.OwnedCarrier{
			MarketID: e.CarrierID, StarSystem: e.StarSystem, SystemAddress: e.SystemAddress,
		})
		return nil
	case journal.EventCarrierJump:
		var e journal.CarrierJumpEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("carrierjump: %w", err)
		}
		// CarrierJump is also a Location-like event for the player when they
		// were riding their carrier through the jump.
		if e.StarSystem != "" && e.SystemAddress != 0 {
			r.Session.SetSystemWithPos(e.StarSystem, e.SystemAddress, e.StarPos)
		}
		// Only register the carrier as owned if we already know it is —
		// docking at someone else's carrier mid-jump shouldn't claim ownership.
		if r.Session.IsOwnedCarrier(e.MarketID) {
			r.Session.RegisterOwnedCarrier(state.OwnedCarrier{
				MarketID: e.MarketID, StarSystem: e.StarSystem, SystemAddress: e.SystemAddress,
			})
		}
		return nil
	case journal.EventMarket:
		var e journal.MarketEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("market: %w", err)
		}
		return r.handleMarket(ctx, e)
	}
	return nil
}

func (r *Reporter) handleCarrierStats(ctx context.Context, e journal.CarrierStatsEvent) error {
	if e.CarrierID == 0 {
		return nil
	}
	r.Session.RegisterOwnedCarrier(state.OwnedCarrier{
		MarketID: e.CarrierID,
		Name:     e.Name,
		Callsign: e.Callsign,
	})
	c, _ := r.Session.OwnedCarrier(e.CarrierID)
	fc := ravencolonial.FleetCarrier{
		MarketID:      c.MarketID,
		Name:          c.Name,
		Callsign:      c.Callsign,
		StarSystem:    c.StarSystem,
		SystemAddress: c.SystemAddress,
	}
	if err := r.API.PutFleetCarrier(ctx, fc); err != nil {
		if errors.Is(err, ravencolonial.ErrNoAPIKey) {
			// Silent skip: FC sync requires an rcc-key, which is optional.
			return nil
		}
		r.emit(LevelError, "Publish FC %s failed: %v", c.Callsign, err)
		return err
	}
	r.emit(LevelOK, "Published Fleet Carrier %s (%s)", c.Name, c.Callsign)
	return nil
}

func (r *Reporter) handleMarket(ctx context.Context, e journal.MarketEvent) error {
	if !r.Session.IsOwnedCarrier(e.MarketID) {
		return nil // not my FC; nothing to sync
	}
	if r.JournalDir == "" {
		r.emit(LevelWarn, "FC cargo sync: journal dir not set; skipping")
		return nil
	}
	read := r.readMarketFile
	if read == nil {
		read = journal.ReadMarketFile
	}
	mf, err := read(r.JournalDir)
	if err != nil {
		r.emit(LevelError, "Read Market.json failed: %v", err)
		return err
	}
	if mf.MarketID != e.MarketID {
		// Market.json races with the journal event briefly; if the file
		// hasn't caught up yet, skip rather than send stale data.
		r.emit(LevelInfo, "Market.json MarketID (%d) doesn't match event (%d); skipping", mf.MarketID, e.MarketID)
		return nil
	}
	cargo := cargoFromMarket(mf)
	if err := r.API.OverwriteCarrierCargo(ctx, e.MarketID, cargo); err != nil {
		if errors.Is(err, ravencolonial.ErrNoAPIKey) {
			return nil
		}
		r.emit(LevelError, "Sync FC cargo for market %d failed: %v", e.MarketID, err)
		return err
	}
	r.emit(LevelOK, "Synced FC cargo (%d commodities, %d total units)", len(cargo), sumValues(cargo))
	return nil
}

// cargoFromMarket builds the {commodity_symbol: stock} map the API wants.
// Items with zero stock are omitted; sending them would just clutter the
// server-side record.
func cargoFromMarket(mf *journal.MarketFile) ravencolonial.Cargo {
	out := ravencolonial.Cargo{}
	for _, it := range mf.Items {
		if it.Stock <= 0 {
			continue
		}
		key := NormalizeCommodity(it.Name)
		if key == "" {
			key = NormalizeCommodity(it.NameLocalised)
		}
		if key == "" {
			continue
		}
		out[key] += it.Stock
	}
	return out
}

func (r *Reporter) handleDepot(ctx context.Context, e journal.ColonisationConstructionDepotEvent) error {
	_, sysAddr := r.Session.System()
	if sysAddr == 0 {
		r.emit(LevelWarn, "Construction depot event but no system address known yet; skipping")
		return nil
	}
	marketID := e.MarketID
	if marketID == 0 {
		// Fall back to the docked market if the event doesn't carry one.
		_, _, marketID = r.Session.Dock()
		if marketID == 0 {
			r.emit(LevelWarn, "Construction depot event with no MarketID; skipping")
			return nil
		}
	}

	buildID, ok := r.Session.BuildFor(marketID)
	if !ok {
		proj, err := r.API.ProjectBySystemMarket(ctx, sysAddr, marketID)
		if err != nil {
			if ravencolonial.IsNotFound(err) {
				r.emit(LevelInfo, "No ravencolonial project yet for market %d in system %d", marketID, sysAddr)
				return nil
			}
			r.emit(LevelError, "Lookup project for market %d failed: %v", marketID, err)
			return err
		}
		if proj == nil || proj.BuildID == "" {
			r.emit(LevelInfo, "Empty project response for market %d; skipping", marketID)
			return nil
		}
		buildID = proj.BuildID
		r.Session.RememberBuild(marketID, buildID)
	}

	commodities, maxNeed := commoditiesFromDepot(e)
	update := ravencolonial.ProjectUpdate{
		BuildID:     buildID,
		Commodities: commodities,
		MaxNeed:     maxNeed,
	}
	if err := r.API.UpdateProject(ctx, update); err != nil {
		r.emit(LevelError, "Update project %s failed: %v", buildID, err)
		return err
	}
	r.emit(LevelOK, "Reported depot %s: %d commodities outstanding", buildID, len(commodities))

	if e.ConstructionComplete {
		if err := r.API.CompleteProject(ctx, buildID); err != nil {
			r.emit(LevelError, "Mark complete %s failed: %v", buildID, err)
			return err
		}
		r.emit(LevelOK, "Marked build %s complete", buildID)
		r.Session.RememberBuild(marketID, "") // drop cache so it doesn't linger
	}
	return nil
}

func (r *Reporter) handleContribution(ctx context.Context, e journal.ColonisationContributionEvent) error {
	cmdr := r.Session.Commander()
	if cmdr == "" {
		r.emit(LevelWarn, "Contribution event but commander unknown; skipping")
		return nil
	}
	marketID := e.MarketID
	if marketID == 0 {
		_, _, marketID = r.Session.Dock()
	}
	if marketID == 0 {
		r.emit(LevelWarn, "Contribution event with no MarketID; skipping")
		return nil
	}
	buildID, ok := r.Session.BuildFor(marketID)
	if !ok {
		// Try to resolve via system+market.
		_, sysAddr := r.Session.System()
		if sysAddr == 0 {
			r.emit(LevelWarn, "Contribution but no system address known")
			return nil
		}
		proj, err := r.API.ProjectBySystemMarket(ctx, sysAddr, marketID)
		if err != nil {
			if ravencolonial.IsNotFound(err) {
				r.emit(LevelInfo, "No project for market %d; cannot attribute contribution", marketID)
				return nil
			}
			return err
		}
		if proj == nil || proj.BuildID == "" {
			r.emit(LevelInfo, "Empty project for market %d; cannot attribute", marketID)
			return nil
		}
		buildID = proj.BuildID
		r.Session.RememberBuild(marketID, buildID)
	}

	contrib := contributionsFromEvent(e)
	if len(contrib) == 0 {
		return nil
	}
	if err := r.API.Contribute(ctx, buildID, cmdr, contrib); err != nil {
		r.emit(LevelError, "Contribute to %s failed: %v", buildID, err)
		return err
	}
	r.emit(LevelOK, "Contributed to %s as %s (%d items)", buildID, cmdr, sumValues(contrib))
	return nil
}

// commoditiesFromDepot converts the ResourcesRequired array into the
// {symbol: outstanding} map the API expects, and computes maxNeed.
func commoditiesFromDepot(e journal.ColonisationConstructionDepotEvent) (map[string]int, int) {
	out := make(map[string]int, len(e.ResourcesRequired))
	max := 0
	for _, r := range e.ResourcesRequired {
		need := r.RequiredAmount - r.ProvidedAmount
		if need < 0 {
			need = 0
		}
		key := NormalizeCommodity(r.Name)
		if key == "" {
			key = NormalizeCommodity(r.NameLocalised)
		}
		if key == "" {
			continue
		}
		out[key] = need
		if need > max {
			max = need
		}
	}
	return out, max
}

func contributionsFromEvent(e journal.ColonisationContributionEvent) ravencolonial.Contribution {
	out := ravencolonial.Contribution{}
	for _, c := range e.Contributions {
		key := NormalizeCommodity(c.Name)
		if key == "" {
			key = NormalizeCommodity(c.NameLocalised)
		}
		if key == "" || c.Amount <= 0 {
			continue
		}
		out[key] += c.Amount
	}
	return out
}

func sumValues(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

// Sentinel for callers that want to distinguish "we knew what to do but the
// API refused" from generic errors.
var ErrAPI = errors.New("ravencolonial API error")
