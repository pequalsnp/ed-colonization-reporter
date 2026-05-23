// Package state holds the in-memory session state derived from journal events.
package state

import (
	"sync"
	"time"
)

// Session captures everything we know about the current Elite Dangerous play
// session: who is playing, where they are, and which construction-site build
// is associated with which MarketID. It is safe for concurrent use.
type Session struct {
	mu            sync.RWMutex
	commander     string
	fid           string
	starSystem    string
	systemAddress int64
	starPos       [3]float64
	hasStarPos    bool
	docked        bool
	marketID      int64
	stationName   string

	// Game version/build/DLC flags, extracted from Fileheader and LoadGame.
	// Required by EDDN uploads.
	gameVersion string
	gameBuild   string
	horizons    *bool
	odyssey     *bool

	// buildByMarket caches the buildId we last resolved for a given MarketID
	// so contribution events can post without an extra lookup.
	buildByMarket map[int64]string

	// ownedCarriers tracks Fleet Carriers the commander owns. The presence
	// of an entry is the source of truth that "this MarketID is my FC".
	ownedCarriers map[int64]OwnedCarrier

	// fcCargo is the per-carrier cargo snapshot the GUI reads for the
	// "FC" column on project cards. Seeded by cAPI polls and Market.json
	// reads; updated incrementally by CargoTransfer and MarketBuy/Sell
	// events so the local view doesn't stall for 15 min between cAPI
	// polls. Keyed by MarketID.
	fcCargo map[int64]map[string]int
	// fcCargoAt records when each MarketID's cargo was last touched.
	fcCargoAt map[int64]time.Time
	// fcCargoSyncedAt is the *game-time* watermark of the data currently
	// cached. When cAPI gives us a snapshot, this is set to the latest
	// CarrierStats timestamp at the time of the fetch — Frontier's cAPI
	// is observed to return state aligned with the most recent
	// CarrierStats event (and can be up to 30+ min stale). When the
	// reporter sees a journal CargoTransfer / MarketBuy / MarketSell
	// event, it applies the delta only if the event timestamp is AFTER
	// this watermark — otherwise the change is presumed already baked
	// into the cached snapshot. This is what stops backfill from
	// double-counting deltas already in cAPI's response.
	fcCargoSyncedAt map[int64]time.Time
	// lastCarrierStatsAt is the timestamp of the most recent CarrierStats
	// event we've seen for a given carrier. Used to anchor cAPI baselines
	// to the game's own checkpoint clock.
	lastCarrierStatsAt map[int64]time.Time

	// shipCargo is the current snapshot of the ship's hold, mirrored
	// from Cargo events. The journal emits a Cargo event after every
	// inventory change, so this stays current without explicit polling.
	shipCargo   map[string]int
	shipCargoAt time.Time
}

// OwnedCarrier is the minimal record we need about a commander's FC to sync it.
type OwnedCarrier struct {
	MarketID      int64
	Name          string
	Callsign      string
	StarSystem    string
	SystemAddress int64
}

// New returns an empty Session.
func New() *Session {
	return &Session{
		buildByMarket:      map[int64]string{},
		ownedCarriers:      map[int64]OwnedCarrier{},
		fcCargo:            map[int64]map[string]int{},
		fcCargoAt:          map[int64]time.Time{},
		fcCargoSyncedAt:    map[int64]time.Time{},
		lastCarrierStatsAt: map[int64]time.Time{},
		shipCargo:          map[string]int{},
	}
}

// SetShipCargo replaces the ship cargo snapshot. Called from the
// reporter on every Cargo event. Pass time.Time{} to default to now.
func (s *Session) SetShipCargo(cargo map[string]int, at time.Time) {
	cp := make(map[string]int, len(cargo))
	for k, v := range cargo {
		if v > 0 {
			cp[k] = v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shipCargo = cp
	if at.IsZero() {
		at = time.Now()
	}
	s.shipCargoAt = at
}

// ShipCargo returns a copy of the cached ship cargo manifest plus the
// timestamp it was last updated. Returns (nil, zero-time) before any
// Cargo event has been seen.
func (s *Session) ShipCargo() (map[string]int, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.shipCargo) == 0 {
		return nil, s.shipCargoAt
	}
	cp := make(map[string]int, len(s.shipCargo))
	for k, v := range s.shipCargo {
		cp[k] = v
	}
	return cp, s.shipCargoAt
}

// SetCommander records the commander identity.
func (s *Session) SetCommander(name, fid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commander = name
	if fid != "" {
		s.fid = fid
	}
}

// Commander returns the current commander name (empty if unknown).
func (s *Session) Commander() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.commander
}

// SetSystem records the current star system.
func (s *Session) SetSystem(name string, address int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starSystem = name
	s.systemAddress = address
}

// SetSystemWithPos is like SetSystem but also records the galactic
// coordinates of the system, which EDDN needs to attach to relayed events.
func (s *Session) SetSystemWithPos(name string, address int64, pos [3]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starSystem = name
	s.systemAddress = address
	s.starPos = pos
	s.hasStarPos = true
}

// System returns the current system name and id64.
func (s *Session) System() (name string, address int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.starSystem, s.systemAddress
}

// StarPos returns the galactic coordinates of the current system and a flag
// indicating whether they are known yet.
func (s *Session) StarPos() ([3]float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.starPos, s.hasStarPos
}

// SetGameVersion records the gameversion/build strings that EDDN headers
// require. Pass empty strings for fields the game didn't supply.
func (s *Session) SetGameVersion(version, build string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != "" {
		s.gameVersion = version
	}
	if build != "" {
		s.gameBuild = build
	}
}

// GameVersion returns the cached game version/build strings.
func (s *Session) GameVersion() (version, build string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gameVersion, s.gameBuild
}

// SetDLCFlags records Horizons/Odyssey availability from LoadGame. Pass nil
// for fields LoadGame didn't supply (older clients don't ship these flags).
// Nil-ness is preserved — EDDN must omit the field rather than send false.
func (s *Session) SetDLCFlags(horizons, odyssey *bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if horizons != nil {
		v := *horizons
		s.horizons = &v
	}
	if odyssey != nil {
		v := *odyssey
		s.odyssey = &v
	}
}

// DLCFlags returns the cached Horizons/Odyssey pointers. Either or both may
// be nil if LoadGame did not supply them.
func (s *Session) DLCFlags() (horizons, odyssey *bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.horizons, s.odyssey
}

// SetDocked records that the player has docked at a station/market.
func (s *Session) SetDocked(stationName string, marketID, systemAddress int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docked = true
	s.stationName = stationName
	s.marketID = marketID
	// Docked events sometimes carry a system address even when we missed the
	// preceding Location/FSDJump (e.g. game started while already docked).
	if systemAddress != 0 {
		s.systemAddress = systemAddress
	}
}

// SetUndocked clears the docked station.
func (s *Session) SetUndocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docked = false
	s.marketID = 0
	s.stationName = ""
}

// Dock returns the current dock state.
func (s *Session) Dock() (docked bool, stationName string, marketID int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docked, s.stationName, s.marketID
}

// RememberBuild caches the buildId resolved for a given marketId.
func (s *Session) RememberBuild(marketID int64, buildID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if buildID == "" {
		delete(s.buildByMarket, marketID)
		return
	}
	s.buildByMarket[marketID] = buildID
}

// BuildFor returns the cached buildId for a marketId, if any.
func (s *Session) BuildFor(marketID int64) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.buildByMarket[marketID]
	return id, ok
}

// RegisterOwnedCarrier records (or updates) a Fleet Carrier the commander owns.
// Calling with a zero MarketID is a no-op.
func (s *Session) RegisterOwnedCarrier(c OwnedCarrier) {
	if c.MarketID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.ownedCarriers[c.MarketID]
	// Merge: don't blank out fields the new record left empty.
	if c.Name == "" {
		c.Name = existing.Name
	}
	if c.Callsign == "" {
		c.Callsign = existing.Callsign
	}
	if c.StarSystem == "" {
		c.StarSystem = existing.StarSystem
	}
	if c.SystemAddress == 0 {
		c.SystemAddress = existing.SystemAddress
	}
	s.ownedCarriers[c.MarketID] = c
}

// IsOwnedCarrier reports whether a given MarketID is one of the commander's
// own Fleet Carriers.
func (s *Session) IsOwnedCarrier(marketID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.ownedCarriers[marketID]
	return ok
}

// OwnedCarrier returns the cached record for a given MarketID.
func (s *Session) OwnedCarrier(marketID int64) (OwnedCarrier, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.ownedCarriers[marketID]
	return c, ok
}

// OwnedCarriers returns a snapshot slice of every owned carrier record.
func (s *Session) OwnedCarriers() []OwnedCarrier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]OwnedCarrier, 0, len(s.ownedCarriers))
	for _, c := range s.ownedCarriers {
		out = append(out, c)
	}
	return out
}

// NoteCarrierStats records the timestamp of a CarrierStats event for a
// given FC. The reporter calls this on every CarrierStats event (live
// AND replayed) so we know the most recent in-game checkpoint time —
// which is where Frontier's cAPI cache anchors its response.
func (s *Session) NoteCarrierStats(marketID int64, at time.Time) {
	if marketID == 0 || at.IsZero() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.lastCarrierStatsAt[marketID]; existing.IsZero() || at.After(existing) {
		s.lastCarrierStatsAt[marketID] = at
	}
}

// LastCarrierStatsAt returns the latest CarrierStats timestamp we've
// seen for a given MarketID, or the zero time if none yet.
func (s *Session) LastCarrierStatsAt(marketID int64) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCarrierStatsAt[marketID]
}

// SetFCCargo overwrites the cached cargo for a given MarketID and
// stamps it with the game-time the snapshot represents. Callers should
// pass the latest CarrierStats timestamp at the time of the cAPI fetch
// when known (since cAPI aligns with CarrierStats); a Market.json read
// can pass the event's own timestamp; otherwise pass time.Now() — that
// just means "apply every future delta on top of this".
//
// Calls with an older syncedAt than what's already cached are ignored,
// so a slow cAPI response can't clobber a fresher Market.json read.
func (s *Session) SetFCCargo(marketID int64, cargo map[string]int, syncedAt time.Time) {
	if marketID == 0 {
		return
	}
	if syncedAt.IsZero() {
		syncedAt = time.Now()
	}
	cp := make(map[string]int, len(cargo))
	for k, v := range cargo {
		if v > 0 {
			cp[k] = v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.fcCargoSyncedAt[marketID]; !existing.IsZero() && syncedAt.Before(existing) {
		return
	}
	s.fcCargo[marketID] = cp
	s.fcCargoSyncedAt[marketID] = syncedAt
	s.fcCargoAt[marketID] = time.Now()
}

// ApplyFCCargoDelta adds a signed delta to the cached FC cargo if the
// event timestamp is AFTER the cached snapshot's watermark. Older
// events are presumed already reflected in the snapshot and silently
// skipped — this is what prevents backfill from double-counting
// deltas that cAPI already included.
//
// Pass time.Time{} (zero) to force-apply regardless of watermark; that
// is the right thing for purely-local state that has no canonical
// snapshot to defer to (effectively never happens today, but keeps the
// helper composable).
//
// Entries that hit 0 or below are pruned.
func (s *Session) ApplyFCCargoDelta(marketID int64, delta map[string]int, eventAt time.Time) {
	if marketID == 0 || len(delta) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !eventAt.IsZero() {
		if synced := s.fcCargoSyncedAt[marketID]; !synced.IsZero() && !eventAt.After(synced) {
			// Already in baseline — don't double-apply.
			return
		}
	}
	cur := s.fcCargo[marketID]
	if cur == nil {
		cur = map[string]int{}
		s.fcCargo[marketID] = cur
	}
	for k, d := range delta {
		cur[k] += d
		if cur[k] <= 0 {
			delete(cur, k)
		}
	}
	s.fcCargoAt[marketID] = time.Now()
	if !eventAt.IsZero() {
		s.fcCargoSyncedAt[marketID] = eventAt
	}
}

// FCCargo returns a copy of the cached cargo for a given MarketID.
// Returns (nil, false) if nothing is cached.
func (s *Session) FCCargo(marketID int64) (map[string]int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.fcCargo[marketID]
	if !ok {
		return nil, false
	}
	cp := make(map[string]int, len(c))
	for k, v := range c {
		cp[k] = v
	}
	return cp, true
}

// FCCargoAggregate returns a single {commodity: qty} map summed across
// every owned carrier's cached cargo, plus the most recent owned
// carrier's name. Used by the GUI when surfacing the "FC" column on
// projects — it doesn't currently distinguish which FC the stock is on.
func (s *Session) FCCargoAggregate() (name string, cargo map[string]int, at time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cargo = map[string]int{}
	var latest time.Time
	var latestName string
	for mid := range s.ownedCarriers {
		for k, v := range s.fcCargo[mid] {
			cargo[k] += v
		}
		if t := s.fcCargoAt[mid]; t.After(latest) {
			latest = t
			c := s.ownedCarriers[mid]
			latestName = c.Name
			if latestName == "" {
				latestName = c.Callsign
			}
		}
	}
	if len(cargo) == 0 {
		return "", nil, time.Time{}
	}
	return latestName, cargo, latest
}

// Snapshot returns a copy of the session's user-visible fields. Useful for
// the UI's status panel.
type Snapshot struct {
	Commander     string
	FID           string
	StarSystem    string
	SystemAddress int64
	Docked        bool
	StationName   string
	MarketID      int64
	GameVersion   string
	GameBuild     string
}

// Snapshot returns the current snapshot.
func (s *Session) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Commander:     s.commander,
		FID:           s.fid,
		StarSystem:    s.starSystem,
		SystemAddress: s.systemAddress,
		Docked:        s.docked,
		StationName:   s.stationName,
		MarketID:      s.marketID,
		GameVersion:   s.gameVersion,
		GameBuild:     s.gameBuild,
	}
}
