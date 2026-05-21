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
	EventCommander                 = "Commander"
	EventLoadGame                  = "LoadGame"
	EventLocation                  = "Location"
	EventFSDJump                   = "FSDJump"
	EventCarrierJump               = "CarrierJump"
	EventDocked                    = "Docked"
	EventUndocked                  = "Undocked"
	EventColonisationConstructionDepot = "ColonisationConstructionDepot"
	EventColonisationContribution      = "ColonisationContribution"
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
type Raw struct {
	Envelope
	Payload []byte
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

// LoadGameEvent corresponds to the "LoadGame" journal event.
type LoadGameEvent struct {
	Envelope
	Commander string `json:"Commander"`
	FID       string `json:"FID"`
}

// LocationLikeEvent covers Location/FSDJump/CarrierJump — events that report
// the player's current star system, including its 64-bit address (id64).
type LocationLikeEvent struct {
	Envelope
	StarSystem    string `json:"StarSystem"`
	SystemAddress int64  `json:"SystemAddress"`
}

// DockedEvent corresponds to the "Docked" journal event.
type DockedEvent struct {
	Envelope
	StationName     string `json:"StationName"`
	StationType     string `json:"StationType"`
	MarketID        int64  `json:"MarketID"`
	SystemAddress   int64  `json:"SystemAddress"`
	StarSystem      string `json:"StarSystem"`
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
