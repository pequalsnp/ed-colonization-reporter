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
	docked        bool
	marketID      int64
	stationName   string

	// buildByMarket caches the buildId we last resolved for a given MarketID
	// so contribution events can post without an extra lookup.
	buildByMarket map[int64]string
}

// New returns an empty Session.
func New() *Session {
	return &Session{buildByMarket: map[int64]string{}}
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

// System returns the current system name and id64.
func (s *Session) System() (name string, address int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.starSystem, s.systemAddress
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
	}
}
