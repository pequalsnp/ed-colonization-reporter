package journal

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event names emitted by Elite Dangerous that this reporter consumes. The
// full journal spec defines hundreds of event types; we only care about the
// ones below.
const (
	EventFileheader                    = "Fileheader"
	EventCommander                     = "Commander"
	EventLoadGame                      = "LoadGame"
	EventLocation                      = "Location"
	EventFSDJump                       = "FSDJump"
	EventCarrierJump                   = "CarrierJump"
	EventCarrierLocation               = "CarrierLocation"
	EventCarrierStats                  = "CarrierStats"
	EventDocked                        = "Docked"
	EventUndocked                      = "Undocked"
	EventMarket                        = "Market"
	EventMarketBuy                     = "MarketBuy"
	EventMarketSell                    = "MarketSell"
	EventCargoTransfer                 = "CargoTransfer"
	EventColonisationConstructionDepot = "ColonisationConstructionDepot"
	EventColonisationContribution      = "ColonisationContribution"
	EventColonisationBeaconDeployed    = "ColonisationBeaconDeployed"
)

// Envelope is the minimal common shape of every journal event line. Parse
// this first to learn the event name and timestamp, then decode the full
// payload into the matching typed struct based on Event.
type Envelope struct {
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"`
}

// Raw is an event line that has been split into its envelope metadata and
// the unparsed JSON payload. Holding the payload lets callers decode into
// whichever event-specific struct they need without re-parsing the line.
//
// Replayed is true when this event was read during the initial backfill
// pass (StartAtBeginning, before we caught up to the file's current end).
// Consumers with non-idempotent server-side effects — `ColonisationContribution`
// would re-attribute, `CargoTransfer` would re-apply a delta — must skip
// these. Events read after the first time we hit EOF are flagged
// Replayed=false.
type Raw struct {
	Envelope
	Payload  []byte
	Replayed bool
}

// ParseLine parses a single journal line. Returns ErrEmptyLine for empty or
// whitespace-only input so callers can cheaply skip them.
func ParseLine(line []byte) (Raw, error) {
	line = trimUTF8BOM(line)
	if len(trimSpace(line)) == 0 {
		return Raw{}, ErrEmptyLine
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Raw{}, fmt.Errorf("parse envelope: %w", err)
	}
	if env.Event == "" {
		return Raw{}, fmt.Errorf("event field missing or empty")
	}
	// Make a defensive copy: callers may keep the Raw around after the input
	// buffer is reused (e.g. by bufio.Scanner).
	payload := make([]byte, len(line))
	copy(payload, line)
	return Raw{Envelope: env, Payload: payload}, nil
}

// ErrEmptyLine is returned by ParseLine for empty or whitespace-only input.
var ErrEmptyLine = fmt.Errorf("empty line")

// CommanderEvent corresponds to the "Commander" journal event, emitted when
// the game session establishes the player's commander identity.
type CommanderEvent struct {
	Envelope
	Name string `json:"Name"`
	FID  string `json:"FID"`
}

// LoadGameEvent corresponds to the "LoadGame" journal event. The optional
// Horizons/Odyssey fields tell us which DLCs are active — EDDN uploads
// require this and the absence of either field must round-trip as
// "unknown" (not false).
type LoadGameEvent struct {
	Envelope
	Commander string `json:"Commander"`
	FID       string `json:"FID"`
	Horizons  *bool  `json:"Horizons,omitempty"`
	Odyssey   *bool  `json:"Odyssey,omitempty"`
	GameVersion string `json:"gameversion"`
	GameBuild   string `json:"build"`
}

// FileheaderEvent is the first entry of every journal file; it carries the
// game version/build strings, which EDDN uploaders are required to relay.
type FileheaderEvent struct {
	Envelope
	GameVersion string `json:"gameversion"`
	GameBuild   string `json:"build"`
}

// LocationLikeEvent covers Location/FSDJump/CarrierJump — events that report
// the player's current star system, including its 64-bit address (id64) and
// galactic coordinates.
type LocationLikeEvent struct {
	Envelope
	StarSystem    string     `json:"StarSystem"`
	SystemAddress int64      `json:"SystemAddress"`
	StarPos       [3]float64 `json:"StarPos"`
}

// DockedEvent corresponds to the "Docked" journal event. The
// StationFaction / Body fields are optional in the journal — Frontier
// only emits them in some contexts (planetary docks always include
// Body+BodyID; orbital docks always include StationFaction.Name).
type DockedEvent struct {
	Envelope
	StationName    string `json:"StationName"`
	StationType    string `json:"StationType"`
	MarketID       int64  `json:"MarketID"`
	SystemAddress  int64  `json:"SystemAddress"`
	StarSystem     string `json:"StarSystem"`
	StationFaction struct {
		Name string `json:"Name"`
	} `json:"StationFaction"`
	Body   string `json:"Body,omitempty"`
	BodyID *int   `json:"BodyID,omitempty"`
}

// UndockedEvent corresponds to the "Undocked" journal event.
type UndockedEvent struct {
	Envelope
	StationName string `json:"StationName"`
	MarketID    int64  `json:"MarketID"`
}

// ResourceRequired is one row of the ResourcesRequired array inside a
// ColonisationConstructionDepot event.
type ResourceRequired struct {
	Name           string `json:"Name"`            // internal symbol, e.g. "$titanium_name;"
	NameLocalised  string `json:"Name_Localised"`  // human-readable, e.g. "Titanium"
	RequiredAmount int    `json:"RequiredAmount"`
	ProvidedAmount int    `json:"ProvidedAmount"`
	Payment        int    `json:"Payment"`
}

// ColonisationConstructionDepotEvent is the primary event we report on. It
// is emitted by the game on docking with a construction site and again on
// interaction. It carries the full current state of required and provided
// resources for the build.
type ColonisationConstructionDepotEvent struct {
	Envelope
	MarketID             int64              `json:"MarketID"`
	ConstructionProgress float64            `json:"ConstructionProgress"`
	ConstructionComplete bool               `json:"ConstructionComplete"`
	ConstructionFailed   bool               `json:"ConstructionFailed"`
	ResourcesRequired    []ResourceRequired `json:"ResourcesRequired"`
}

// Contribution is one row inside the ColonisationContribution event.
type Contribution struct {
	Name          string `json:"Name"`
	NameLocalised string `json:"Name_Localised"`
	Amount        int    `json:"Amount"`
}

// ColonisationContributionEvent is emitted when the commander delivers
// cargo to a construction depot. We use it to attribute the contribution
// to the commander on the server.
type ColonisationContributionEvent struct {
	Envelope
	MarketID      int64          `json:"MarketID"`
	Contributions []Contribution `json:"Contributions"`
}

// ColonisationBeaconDeployedEvent fires when the commander drops a
// colonisation beacon — Frontier's way of claiming a system as
// architect. ravencolonial uses this to set the cmdr as the system's
// architect record.
type ColonisationBeaconDeployedEvent struct {
	Envelope
	SystemAddress int64  `json:"SystemAddress"`
	StarSystem    string `json:"StarSystem"`
}

// CarrierStatsEvent is emitted on game start (and on demand) with the full
// state of the commander's owned Fleet Carrier. CarrierID == MarketID.
type CarrierStatsEvent struct {
	Envelope
	CarrierID     int64  `json:"CarrierID"`
	Callsign      string `json:"Callsign"`
	Name          string `json:"Name"`
	DockingAccess string `json:"DockingAccess"`
}

// CarrierLocationEvent is emitted when the player's carrier arrives in a
// new system. Confirms ownership (game only emits this for your carrier)
// and gives us the current system.
type CarrierLocationEvent struct {
	Envelope
	CarrierID     int64  `json:"CarrierID"`
	StarSystem    string `json:"StarSystem"`
	SystemAddress int64  `json:"SystemAddress"`
}

// CarrierJumpEvent fires when the player is docked on their carrier during
// a jump — it's a Location-like event with carrier-specific fields.
type CarrierJumpEvent struct {
	Envelope
	Docked        bool       `json:"Docked"`
	StationName   string     `json:"StationName"`
	StationType   string     `json:"StationType"`
	MarketID      int64      `json:"MarketID"`
	StarSystem    string     `json:"StarSystem"`
	SystemAddress int64      `json:"SystemAddress"`
	StarPos       [3]float64 `json:"StarPos"`
}

// MarketEvent is the brief journal-side record that fires when the player
// opens a station's commodities market. The full inventory is written to
// Market.json — see internal/journal Market.json reader.
type MarketEvent struct {
	Envelope
	MarketID    int64  `json:"MarketID"`
	StationName string `json:"StationName"`
	StationType string `json:"StationType"`
	StarSystem  string `json:"StarSystem"`
}

// CargoTransferDirection labels each row in a CargoTransfer event. Values
// are documented as lowercase strings in the journal manual.
const (
	TransferToCarrier = "tocarrier"
	TransferToShip    = "toship"
	TransferToSRV     = "tosrv"
)

// CargoTransferItem is one row of a CargoTransfer event.
type CargoTransferItem struct {
	Type          string `json:"Type"`           // short symbol, e.g. "cmmcomposite"
	TypeLocalised string `json:"Type_Localised"` // "CMM Composite"
	Count         int    `json:"Count"`
	Direction     string `json:"Direction"`      // "tocarrier" | "toship" | "tosrv"
	MissionID     int64  `json:"MissionID,omitempty"`
}

// CargoTransferEvent fires when the player moves cargo between the ship,
// an FC, or an SRV. The event has no MarketID; the caller infers the FC
// from the player's current dock state.
type CargoTransferEvent struct {
	Envelope
	Transfers []CargoTransferItem `json:"Transfers"`
}

// MarketBuyEvent fires when the player buys commodities at a station's
// commodities market. At an owned FC this means cargo leaves the FC.
type MarketBuyEvent struct {
	Envelope
	MarketID      int64  `json:"MarketID"`
	Type          string `json:"Type"`
	TypeLocalised string `json:"Type_Localised"`
	Count         int    `json:"Count"`
	BuyPrice      int    `json:"BuyPrice"`
	TotalCost     int    `json:"TotalCost"`
}

// MarketSellEvent fires when the player sells commodities at a station's
// commodities market. At an owned FC this means cargo arrives at the FC.
type MarketSellEvent struct {
	Envelope
	MarketID      int64  `json:"MarketID"`
	Type          string `json:"Type"`
	TypeLocalised string `json:"Type_Localised"`
	Count         int    `json:"Count"`
	SellPrice     int    `json:"SellPrice"`
	TotalSale     int    `json:"TotalSale"`
}

// trimUTF8BOM strips a leading UTF-8 BOM (EF BB BF) if present. Frontier
// writes journals with a BOM on the first line.
func trimUTF8BOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
