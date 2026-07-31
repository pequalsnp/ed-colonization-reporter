// Package edcolonize is an OPTIONAL integration with a self-hosted
// [edcolonize] instance — a colonization-candidate tool that also exposes an
// MCP server, so an AI assistant can query it. This destination feeds that
// instance the player's live commander state, letting the assistant ground
// its advice in what is actually true of the game right now instead of
// asking the player to recite it.
//
// Every other destination in this repo reports OUTWARD to a community
// service (ravencolonial, EDDN, EDSM, Inara) and is about contributing data.
// This one reports INWARD, to a server the player runs themselves: where
// they are, what they're flying, what they're carrying, what a construction
// site still wants. That data never leaves their machine.
//
// # Entirely optional
//
// The reporter does not need edcolonize to function, and most users will
// never run one. This destination is disabled unless the user explicitly
// configures a URL: with no configuration, New returns an Uploader whose
// HandleEvent immediately returns [destinations.ErrDisabled] and which opens
// no sockets and starts no goroutines. Nothing else in the reporter reads
// from it or depends on it.
//
// # Wire contract
//
// The types below MIRROR github.com/pequalsnp/edcolonize's internal/cmdr
// package. They can't be imported — separate module, private repo — so they
// are duplicated deliberately. If you change a json tag here, change it there
// in the same change and bump SchemaVersion on both sides; the receiver
// rejects an unknown schema version with 400 rather than storing a
// half-understood snapshot. types_test.go pins the tags against accidental
// renames.
//
// [edcolonize]: https://github.com/pequalsnp/edcolonize
package edcolonize

import "time"

// SchemaVersion is the contract version carried in every snapshot. Must match
// the constant of the same name in edcolonize's internal/cmdr.
const SchemaVersion = 1

// Snapshot is one complete picture of commander state at a moment in time.
// Everything except Commander and CapturedAt is optional — snapshots are
// built incrementally as journal events land.
type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	CapturedAt    time.Time `json:"captured_at"`
	Commander     string    `json:"commander"`
	FID           string    `json:"fid,omitempty"`
	GameVersion   string    `json:"game_version,omitempty"`
	GameBuild     string    `json:"game_build,omitempty"`
	Horizons      *bool     `json:"horizons,omitempty"`
	Odyssey       *bool     `json:"odyssey,omitempty"`

	Location     Location    `json:"location"`
	Ship         *Ship       `json:"ship,omitempty"`
	Credits      *int64      `json:"credits,omitempty"`
	Ranks        *Ranks      `json:"ranks,omitempty"`
	Cargo        []CargoItem `json:"cargo,omitempty"`
	Materials    *Materials  `json:"materials,omitempty"`
	Missions     []Mission   `json:"missions,omitempty"`
	Colonisation []Depot     `json:"colonisation,omitempty"`
	Carriers     []Carrier   `json:"carriers,omitempty"`
}

// Location is where the commander is right now.
type Location struct {
	StarSystem    string      `json:"star_system,omitempty"`
	SystemAddress int64       `json:"system_address,omitempty"`
	StarPos       *[3]float64 `json:"star_pos,omitempty"`
	Body          string      `json:"body,omitempty"`
	Docked        bool        `json:"docked"`
	StationName   string      `json:"station_name,omitempty"`
	StationType   string      `json:"station_type,omitempty"`
	MarketID      int64       `json:"market_id,omitempty"`
}

// Ship is a loadout summary, not the raw Loadout blob.
type Ship struct {
	Type          string   `json:"type,omitempty"`
	Name          string   `json:"name,omitempty"`
	Ident         string   `json:"ident,omitempty"`
	HullValue     *int64   `json:"hull_value,omitempty"`
	Rebuy         *int64   `json:"rebuy,omitempty"`
	CargoCapacity *int     `json:"cargo_capacity,omitempty"`
	FuelCapacity  *float64 `json:"fuel_capacity,omitempty"`
	FuelLevel     *float64 `json:"fuel_level,omitempty"`
	MaxJumpRange  *float64 `json:"max_jump_range,omitempty"`
	Modules       []Module `json:"modules,omitempty"`
}

// Module is one fitted module, with its engineering state if any.
type Module struct {
	Slot       string `json:"slot"`
	Item       string `json:"item"`
	On         bool   `json:"on"`
	Blueprint  string `json:"blueprint,omitempty"`
	Grade      int    `json:"grade,omitempty"`
	Experiment string `json:"experimental_effect,omitempty"`
}

// CargoItem is one commodity in the hold.
type CargoItem struct {
	Name          string `json:"name"`
	LocalisedName string `json:"localised_name,omitempty"`
	Count         int    `json:"count"`
	Stolen        int    `json:"stolen,omitempty"`
}

// Materials are engineering materials on hand, by category.
type Materials struct {
	Raw          []MaterialItem `json:"raw,omitempty"`
	Manufactured []MaterialItem `json:"manufactured,omitempty"`
	Encoded      []MaterialItem `json:"encoded,omitempty"`
}

// MaterialItem is one material and how many are held.
type MaterialItem struct {
	Name          string `json:"name"`
	LocalisedName string `json:"localised_name,omitempty"`
	Count         int    `json:"count"`
}

// Mission is one accepted, not-yet-resolved mission.
type Mission struct {
	MissionID          int64      `json:"mission_id"`
	Name               string     `json:"name,omitempty"`
	LocalisedName      string     `json:"localised_name,omitempty"`
	Faction            string     `json:"faction,omitempty"`
	DestinationSystem  string     `json:"destination_system,omitempty"`
	DestinationStation string     `json:"destination_station,omitempty"`
	Commodity          string     `json:"commodity,omitempty"`
	Count              int        `json:"count,omitempty"`
	Reward             int64      `json:"reward,omitempty"`
	Expiry             *time.Time `json:"expiry,omitempty"`
	AcceptedAt         time.Time  `json:"accepted_at"`
}

// Depot is a colonisation construction site and its per-commodity state.
type Depot struct {
	MarketID             int64           `json:"market_id"`
	StarSystem           string          `json:"star_system,omitempty"`
	SystemAddress        int64           `json:"system_address,omitempty"`
	StationName          string          `json:"station_name,omitempty"`
	ConstructionProgress float64         `json:"construction_progress"`
	ConstructionComplete bool            `json:"construction_complete"`
	ConstructionFailed   bool            `json:"construction_failed"`
	Resources            []DepotResource `json:"resources,omitempty"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// DepotResource is one commodity a construction site still wants.
type DepotResource struct {
	Name          string `json:"name"`
	LocalisedName string `json:"localised_name,omitempty"`
	Required      int    `json:"required"`
	Provided      int    `json:"provided"`
	Payment       int64  `json:"payment,omitempty"`
}

// Carrier is an owned fleet carrier and its cargo, when known.
type Carrier struct {
	MarketID      int64          `json:"market_id"`
	Name          string         `json:"name,omitempty"`
	Callsign      string         `json:"callsign,omitempty"`
	StarSystem    string         `json:"star_system,omitempty"`
	SystemAddress int64          `json:"system_address,omitempty"`
	FuelLevel     *int           `json:"fuel_level,omitempty"`
	Cargo         []CargoItem    `json:"cargo,omitempty"`
	Balance       *int64         `json:"balance,omitempty"`
	Services      map[string]any `json:"services,omitempty"`
}

// Ranks are the commander's progression ranks.
type Ranks struct {
	Combat       int `json:"combat"`
	Trade        int `json:"trade"`
	Explore      int `json:"explore"`
	Soldier      int `json:"soldier,omitempty"`
	Exobiologist int `json:"exobiologist,omitempty"`
	CQC          int `json:"cqc,omitempty"`
	Empire       int `json:"empire"`
	Federation   int `json:"federation"`

	CombatProgress     int `json:"combat_progress,omitempty"`
	TradeProgress      int `json:"trade_progress,omitempty"`
	ExploreProgress    int `json:"explore_progress,omitempty"`
	EmpireProgress     int `json:"empire_progress,omitempty"`
	FederationProgress int `json:"federation_progress,omitempty"`
}
