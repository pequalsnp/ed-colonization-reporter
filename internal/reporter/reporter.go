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
	"strings"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// APIClient is the subset of ravencolonial.Client the reporter uses. Defining
// it as an interface lets tests substitute a fake.
type APIClient interface {
	ProjectBySystemMarket(ctx context.Context, systemAddress, marketID int64) (*ravencolonial.Project, error)
	CreateProject(ctx context.Context, p ravencolonial.ProjectCreate) (*ravencolonial.Project, error)
	UpdateProject(ctx context.Context, update ravencolonial.ProjectUpdate) error
	CompleteProject(ctx context.Context, buildID string) error
	Contribute(ctx context.Context, buildID, cmdr string, contrib ravencolonial.Contribution) error
	PutFleetCarrier(ctx context.Context, fc ravencolonial.FleetCarrier) error
	OverwriteCarrierCargo(ctx context.Context, marketID int64, cargo ravencolonial.Cargo) error
	PatchCarrierCargo(ctx context.Context, marketID int64, delta ravencolonial.Cargo) error
	SetSystemArchitect(ctx context.Context, systemName, cmdr string) error
	PatchProject(ctx context.Context, buildID string, patch ravencolonial.ProjectPatch) error
	CommanderCarriers(ctx context.Context, cmdr string) ([]ravencolonial.LinkedCarrier, error)
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

	// linkedCarriersFetchedFor tracks which commander we last pulled the
	// /api/cmdr/{cmdr}/fc/all list for. Only fetch once per commander
	// switch — the list rarely changes mid-session.
	linkedCarriersFetchedFor string
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
		// Live (not replayed) commander events kick off the one-time
		// linked-carrier fetch so we recognise FCs that were linked via
		// the website before this session started.
		if !raw.Replayed && e.Name != "" && r.linkedCarriersFetchedFor != e.Name {
			r.linkedCarriersFetchedFor = e.Name
			go r.fetchLinkedCarriers(context.Background(), e.Name)
		}
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
		// Note: previously we POSTed a sparse PatchProject here to
		// refresh faction/body, but ravencolonial returned 400 — it
		// likely requires the full ProjectUpdate body shape. Backed
		// out until the contract is understood; depot events that
		// arrive shortly after dock already carry the data we need.
	case journal.EventUndocked:
		r.Session.SetUndocked()
	case journal.EventColonisationConstructionDepot:
		var e journal.ColonisationConstructionDepotEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("depot: %w", err)
		}
		return r.handleDepot(ctx, e)
	case journal.EventColonisationContribution:
		if raw.Replayed {
			// Skipping replayed contributions: ravencolonial's
			// /contribute endpoint accumulates per-call. Re-firing it on
			// every backfill would double-credit the commander.
			return nil
		}
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
	case journal.EventColonisationBeaconDeployed:
		if raw.Replayed {
			return nil // setting architect is server-side immutable-ish
		}
		var e journal.ColonisationBeaconDeployedEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("beacon: %w", err)
		}
		return r.handleBeaconDeployed(ctx, e)
	case journal.EventCargoTransfer:
		if raw.Replayed {
			// Skipping replayed cargo transfers: PatchCarrierCargo applies
			// a server-side delta. Re-firing on every backfill multiplies
			// FC cargo on ravencolonial by the number of restarts.
			return nil
		}
		var e journal.CargoTransferEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("cargotransfer: %w", err)
		}
		return r.handleCargoTransfer(ctx, e)
	case journal.EventMarketBuy:
		if raw.Replayed {
			return nil // same accumulation rationale as CargoTransfer
		}
		var e journal.MarketBuyEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("marketbuy: %w", err)
		}
		// Buying from a market: cargo leaves the station. If it's our FC,
		// that's a negative delta against our FC stock.
		return r.handleFCMarketDelta(ctx, e.MarketID, e.Type, e.TypeLocalised, -e.Count, "buy")
	case journal.EventMarketSell:
		if raw.Replayed {
			return nil
		}
		var e journal.MarketSellEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return fmt.Errorf("marketsell: %w", err)
		}
		// Selling to a market: cargo arrives at the station. If it's our
		// FC, that's a positive delta.
		return r.handleFCMarketDelta(ctx, e.MarketID, e.Type, e.TypeLocalised, +e.Count, "sell")
	}
	return nil
}

// fetchLinkedCarriers pulls the commander's website-linked FCs from
// ravencolonial and registers them in the session so subsequent
// CargoTransfer / MarketBuy / MarketSell events at those FCs are
// recognised even before the in-game CarrierStats arrives.
func (r *Reporter) fetchLinkedCarriers(ctx context.Context, cmdr string) {
	cs, err := r.API.CommanderCarriers(ctx, cmdr)
	if err != nil {
		r.emit(LevelWarn, "Fetch linked FCs for %s failed: %v", cmdr, err)
		return
	}
	for _, c := range cs {
		if c.MarketID == 0 {
			continue
		}
		r.Session.RegisterOwnedCarrier(state.OwnedCarrier{
			MarketID:   c.MarketID,
			Name:       c.Name,
			Callsign:   c.Callsign,
			StarSystem: c.StarSystem,
		})
	}
	if len(cs) > 0 {
		r.emit(LevelInfo, "Loaded %d linked FC(s) from ravencolonial", len(cs))
	}
}

// refreshProjectMetadata posts a sparse update with faction/body data
// to a known project. SrvSurvey does this on every Docked event so the
// website's project record stays accurate when the cmdr settles the
// station's faction or the build is relocated to a different body.
func (r *Reporter) refreshProjectMetadata(ctx context.Context, buildID string, e journal.DockedEvent) {
	patch := ravencolonial.ProjectPatch{
		FactionName: e.StationFaction.Name,
		BodyName:    e.Body,
		BodyNum:     e.BodyID,
	}
	if patch.FactionName == "" && patch.BodyName == "" && patch.BodyNum == nil {
		return
	}
	if err := r.API.PatchProject(ctx, buildID, patch); err != nil {
		r.emit(LevelWarn, "Refresh project %s metadata failed: %v", buildID, err)
	}
}

// handleBeaconDeployed records the commander as architect of the system
// where they just dropped the colonisation beacon.
func (r *Reporter) handleBeaconDeployed(ctx context.Context, e journal.ColonisationBeaconDeployedEvent) error {
	system := e.StarSystem
	if system == "" {
		system, _ = r.Session.System()
	}
	cmdr := r.Session.Commander()
	if system == "" || cmdr == "" {
		return nil
	}
	if err := r.API.SetSystemArchitect(ctx, system, cmdr); err != nil {
		if errors.Is(err, ravencolonial.ErrNoAPIKey) {
			r.emit(LevelInfo, "Beacon deployed in %s — set architect on ravencolonial requires rcc-key (skipped)", system)
			return nil
		}
		r.emit(LevelError, "Set architect for %s failed: %v", system, err)
		return err
	}
	r.emit(LevelOK, "Set %s as architect of %s on ravencolonial", cmdr, system)
	return nil
}

// handleFCMarketDelta turns a MarketBuy or MarketSell at a known
// commander-owned FC into a delta PATCH against ravencolonial's FC
// cargo. Transactions at other stations are ignored. Count is signed:
// negative for buys (cargo leaves the FC), positive for sells.
func (r *Reporter) handleFCMarketDelta(ctx context.Context, marketID int64, typeSym, typeLocal string, count int, op string) error {
	if marketID == 0 || count == 0 {
		return nil
	}
	if !r.Session.IsOwnedCarrier(marketID) {
		return nil // not our FC; irrelevant
	}
	key := NormalizeCommodity(typeSym)
	if key == "" {
		key = NormalizeCommodity(typeLocal)
	}
	if key == "" {
		return nil
	}
	delta := ravencolonial.Cargo{key: count}
	if err := r.API.PatchCarrierCargo(ctx, marketID, delta); err != nil {
		if errors.Is(err, ravencolonial.ErrNoAPIKey) {
			return nil
		}
		r.emit(LevelError, "FC market %s delta failed: %v", op, err)
		return err
	}
	r.emit(LevelOK, "FC market %s posted (%s %+d)", op, key, count)
	return nil
}

// handleCargoTransfer turns a journal cargo transfer (ship↔FC) into a
// delta PATCH to ravencolonial. The journal does not say which carrier
// the player is at, so we infer from the current dock — and only PATCH
// when that's one of the commander's own FCs.
func (r *Reporter) handleCargoTransfer(ctx context.Context, e journal.CargoTransferEvent) error {
	_, _, marketID := r.Session.Dock()
	if marketID == 0 || !r.Session.IsOwnedCarrier(marketID) {
		return nil // not at our FC; transfers between ship/SRV don't affect FC state
	}
	delta := ravencolonial.Cargo{}
	for _, t := range e.Transfers {
		if t.Count <= 0 {
			continue
		}
		key := NormalizeCommodity(t.Type)
		if key == "" {
			key = NormalizeCommodity(t.TypeLocalised)
		}
		if key == "" {
			continue
		}
		switch t.Direction {
		case journal.TransferToCarrier:
			delta[key] += t.Count
		case journal.TransferToShip:
			delta[key] -= t.Count
		default:
			// tosrv or future directions — not an FC delta.
		}
	}
	if len(delta) == 0 {
		return nil
	}
	if err := r.API.PatchCarrierCargo(ctx, marketID, delta); err != nil {
		if errors.Is(err, ravencolonial.ErrNoAPIKey) {
			return nil
		}
		r.emit(LevelError, "FC cargo delta failed: %v", err)
		return err
	}
	r.emit(LevelOK, "FC cargo delta posted (%d commodity changes)", len(delta))
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
				// Cold-start case: ravencolonial doesn't know about this
				// depot yet. Create it so subsequent updates have a
				// project to attach to. Replayed events skip this — we
				// don't want backfill to spam new projects.
				if r.Session.Commander() == "" {
					r.emit(LevelInfo, "Skipping project creation for market %d: commander not known yet", marketID)
					return nil
				}
				created, cerr := r.createProjectFromDepot(ctx, e, sysAddr, marketID)
				if cerr != nil {
					r.emit(LevelError, "Create ravencolonial project for market %d failed: %v", marketID, cerr)
					return cerr
				}
				if created == nil || created.BuildID == "" {
					r.emit(LevelWarn, "Create project for market %d returned no BuildID", marketID)
					return nil
				}
				buildID = created.BuildID
				r.Session.RememberBuild(marketID, buildID)
				r.emit(LevelOK, "Created ravencolonial project %s (%s)", created.BuildName, buildID)
				// Fall through — we just sent the depot snapshot in the
				// create body, no need to also POST an update for the
				// same data.
				return r.handleDepotComplete(ctx, e, buildID, marketID)
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
// {symbol: outstanding} map ravencolonial expects, plus maxNeed = sum of
// RequiredAmount (project total size, not outstanding).
//
// SrvSurvey populates maxNeed the same way (FormNewProject.cs:209) and
// ravencolonial uses it for progress display.
func commoditiesFromDepot(e journal.ColonisationConstructionDepotEvent) (map[string]int, int) {
	out := make(map[string]int, len(e.ResourcesRequired))
	total := 0
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
		total += r.RequiredAmount
	}
	return out, total
}

// deriveBuildName turns the in-game StationName into the human-readable
// project name SrvSurvey populates by default (ColonyData.cs:37-49).
// Colonisation-ship docks become "Primary port"; "Orbital Construction
// Site: Foo" → "Foo"; "Planetary Construction Site: Foo" → "Foo".
func deriveBuildName(stationName string) string {
	const colonisationShipPrefix = "$EXT_PANEL_ColonisationShip"
	if stationName == "System Colonisation Ship" ||
		strings.HasPrefix(stationName, colonisationShipPrefix) ||
		strings.HasPrefix(stationName, "$EXT_PNL_ColonisationShip") {
		return "Primary port"
	}
	name := stationName
	name = strings.Replace(name, "$EXT_PANEL_ColonisationShip; ", "", 1)
	name = strings.Replace(name, "Orbital Construction Site:", "", 1)
	name = strings.Replace(name, "Planetary Construction Site:", "", 1)
	return strings.TrimSpace(name)
}

// isPrimaryPort reports whether the dock is the system's first build —
// i.e. the colonisation ship rather than a regular construction depot.
func isPrimaryPort(stationName string) bool {
	return stationName == "System Colonisation Ship" ||
		strings.HasPrefix(stationName, "$EXT_PANEL_ColonisationShip") ||
		strings.HasPrefix(stationName, "$EXT_PNL_ColonisationShip")
}

// createProjectFromDepot builds a ProjectCreate payload from the depot
// event + session state and PUTs it to ravencolonial.
func (r *Reporter) createProjectFromDepot(ctx context.Context, e journal.ColonisationConstructionDepotEvent, sysAddr, marketID int64) (*ravencolonial.Project, error) {
	commodities, maxNeed := commoditiesFromDepot(e)
	systemName, _ := r.Session.System()
	starPos, _ := r.Session.StarPos()
	_, stationName, _ := r.Session.Dock()
	cmdr := r.Session.Commander()

	primary := isPrimaryPort(stationName)
	buildName := deriveBuildName(stationName)
	if buildName == "" {
		buildName = "Unknown build"
	}
	// We can't derive the precise architectural subtype from the journal —
	// SrvSurvey requires the user to pick it from a dropdown. Send a
	// placeholder that's clear when the user inspects on the website.
	buildType := "unknown"
	if primary {
		buildType = "primary-port"
	}

	p := ravencolonial.ProjectCreate{
		BuildType:     buildType,
		BuildName:     buildName,
		MarketID:      marketID,
		SystemAddress: sysAddr,
		SystemName:    systemName,
		StarPos:       starPos,
		MaxNeed:       maxNeed,
		IsPrimaryPort: primary,
		Commodities:   commodities,
		ArchitectName: cmdr,
		Commanders:    map[string][]string{cmdr: {}},
	}
	// Embed the depot event so the website has the full snapshot if
	// SrvSurvey-style consumers need it.
	var raw map[string]any
	if err := json.Unmarshal(rawPayloadOrNil(e), &raw); err == nil && len(raw) > 0 {
		p.ColonisationConstructionDepot = raw
	}
	return r.API.CreateProject(ctx, p)
}

// rawPayloadOrNil returns the JSON-encoded depot event suitable for
// embedding. We re-marshal because the caller has the typed struct, not
// the original payload bytes.
func rawPayloadOrNil(e journal.ColonisationConstructionDepotEvent) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return b
}

// handleDepotComplete handles the "ConstructionComplete=true" follow-up
// after a project was just created. Shared between the create path and
// the regular update path.
func (r *Reporter) handleDepotComplete(ctx context.Context, e journal.ColonisationConstructionDepotEvent, buildID string, marketID int64) error {
	if !e.ConstructionComplete {
		return nil
	}
	if err := r.API.CompleteProject(ctx, buildID); err != nil {
		r.emit(LevelError, "Mark complete %s failed: %v", buildID, err)
		return err
	}
	r.emit(LevelOK, "Marked build %s complete", buildID)
	r.Session.RememberBuild(marketID, "")
	return nil
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
