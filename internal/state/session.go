// Package state holds the in-memory session state derived from journal events.
package state

import "sync"

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
		buildByMarket: map[int64]string{},
		ownedCarriers: map[int64]OwnedCarrier{},
	}
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
