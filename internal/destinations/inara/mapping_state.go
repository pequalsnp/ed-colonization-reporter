package inara

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// isoTime formats a journal timestamp the way Inara expects (UTC, seconds).
func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// pilotRanks maps the journal Rank/Progress field names to the Inara
// rankName values for setCommanderRankPilot.
var pilotRanks = []struct{ field, name string }{
	{"Combat", "combat"}, {"Trade", "trade"}, {"Explore", "explore"},
	{"CQC", "cqc"}, {"Soldier", "soldier"}, {"Exobiologist", "exobiologist"},
	{"Federation", "federation"}, {"Empire", "empire"},
}

// majorFactions maps journal Reputation field names to Inara majorfactionName.
var majorFactions = []struct{ field, name string }{
	{"Empire", "empire"}, {"Federation", "federation"},
	{"Alliance", "alliance"}, {"Independent", "independent"},
}

// mapRank turns a Rank event into setCommanderRankPilot rows carrying the
// rank values. Progress (the 0-100% to the next rank) arrives in a separate
// Progress event and is sent value-free — Inara merges the two by timestamp.
func mapRank(raw journal.Raw) ([]Event, error) {
	var m map[string]any
	if err := json.Unmarshal(raw.Payload, &m); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(pilotRanks))
	for _, r := range pilotRanks {
		v, ok := numField(m, r.field)
		if !ok {
			continue
		}
		rows = append(rows, map[string]any{"rankName": r.name, "rankValue": int(v)})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []Event{{Name: EventSetCommanderRankPilot, Timestamp: isoTime(raw.Timestamp), Data: rows}}, nil
}

// mapProgress turns a Progress event into setCommanderRankPilot rows carrying
// the fractional rank progress ([0..1]).
func mapProgress(raw journal.Raw) ([]Event, error) {
	var m map[string]any
	if err := json.Unmarshal(raw.Payload, &m); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(pilotRanks))
	for _, r := range pilotRanks {
		v, ok := numField(m, r.field)
		if !ok {
			continue
		}
		rows = append(rows, map[string]any{"rankName": r.name, "rankProgress": v / 100.0})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []Event{{Name: EventSetCommanderRankPilot, Timestamp: isoTime(raw.Timestamp), Data: rows}}, nil
}

// mapReputation turns a Reputation event into setCommanderReputationMajorFaction
// rows. Journal reputation is a -100..100 percentage; Inara wants -1..1.
func mapReputation(raw journal.Raw) ([]Event, error) {
	var m map[string]any
	if err := json.Unmarshal(raw.Payload, &m); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(majorFactions))
	for _, f := range majorFactions {
		v, ok := numField(m, f.field)
		if !ok {
			continue
		}
		rows = append(rows, map[string]any{
			"majorfactionName":       f.name,
			"majorfactionReputation": v / 100.0,
		})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []Event{{Name: EventSetCommanderReputationMajorFaction, Timestamp: isoTime(raw.Timestamp), Data: rows}}, nil
}

// minorFaction is one entry of the Factions array in FSDJump/Location.
type minorFaction struct {
	Name         string  `json:"Name"`
	MyReputation float64 `json:"MyReputation"`
}

// mapMinorFactionReputation extracts per-minor-faction reputation from a
// FSDJump/Location payload's Factions array. Only factions the commander has
// a non-zero standing with are reported (zero standing = no relationship).
func mapMinorFactionReputation(raw journal.Raw) []Event {
	var e struct {
		Factions []minorFaction `json:"Factions"`
	}
	if err := json.Unmarshal(raw.Payload, &e); err != nil || len(e.Factions) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(e.Factions))
	for _, f := range e.Factions {
		if f.Name == "" || f.MyReputation == 0 {
			continue
		}
		rows = append(rows, map[string]any{
			"minorfactionName":       f.Name,
			"minorfactionReputation": f.MyReputation / 100.0,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return []Event{{Name: EventSetCommanderReputationMinorFaction, Timestamp: isoTime(raw.Timestamp), Data: rows}}
}

// mapCredits turns a LoadGame event into setCommanderCredits. Per Inara's
// guidance we only send this on session start (LoadGame), not on every
// credit change, to avoid spamming the player's credits log.
func mapCredits(raw journal.Raw) ([]Event, error) {
	var e journal.LoadGameEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if e.Credits == 0 {
		return nil, nil
	}
	return []Event{{
		Name:      EventSetCommanderCredits,
		Timestamp: isoTime(raw.Timestamp),
		Data: map[string]any{
			"commanderCredits": e.Credits,
			"commanderLoan":    e.Loan,
		},
	}}, nil
}

// loadoutEvent is the subset of the Loadout journal event we relay to Inara.
type loadoutEvent struct {
	journal.Envelope
	Ship          string  `json:"Ship"`
	ShipID        int64   `json:"ShipID"`
	ShipName      string  `json:"ShipName"`
	ShipIdent     string  `json:"ShipIdent"`
	HullValue     int64   `json:"HullValue"`
	ModulesValue  int64   `json:"ModulesValue"`
	Rebuy         int64   `json:"Rebuy"`
	MaxJumpRange  float64 `json:"MaxJumpRange"`
	CargoCapacity int     `json:"CargoCapacity"`
}

// mapLoadout turns a Loadout event into setCommanderShip (ship identity +
// values). The full per-module loadout (setCommanderShipLoadout) is a
// separate, more complex event we don't yet emit.
func mapLoadout(raw journal.Raw) ([]Event, error) {
	var e loadoutEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if e.Ship == "" || e.ShipID == 0 {
		return nil, nil
	}
	data := map[string]any{
		"shipType":      e.Ship,
		"shipGameID":    e.ShipID,
		"isCurrentShip": true,
	}
	if e.ShipName != "" {
		data["shipName"] = e.ShipName
	}
	if e.ShipIdent != "" {
		data["shipIdent"] = e.ShipIdent
	}
	if e.HullValue > 0 {
		data["shipHullValue"] = e.HullValue
	}
	if e.ModulesValue > 0 {
		data["shipModulesValue"] = e.ModulesValue
	}
	if e.Rebuy > 0 {
		data["shipRebuyCost"] = e.Rebuy
	}
	if e.MaxJumpRange > 0 {
		data["shipMaxJumpRange"] = e.MaxJumpRange
	}
	if e.CargoCapacity > 0 {
		data["shipCargoCapacity"] = e.CargoCapacity
	}
	return []Event{{Name: EventSetCommanderShip, Timestamp: isoTime(raw.Timestamp), Data: data}}, nil
}

// nameCount is the {Name,Count} shape shared by Materials and Cargo entries.
type nameCount struct {
	Name  string `json:"Name"`
	Count int    `json:"Count"`
}

// materialsEvent is the Materials journal event (the full crafting inventory).
type materialsEvent struct {
	journal.Envelope
	Raw          []nameCount `json:"Raw"`
	Manufactured []nameCount `json:"Manufactured"`
	Encoded      []nameCount `json:"Encoded"`
}

// mapMaterials turns a Materials event into setCommanderInventoryMaterials,
// which replaces the whole materials inventory in one shot.
func mapMaterials(raw journal.Raw) ([]Event, error) {
	var e materialsEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(e.Raw)+len(e.Manufactured)+len(e.Encoded))
	for _, group := range [][]nameCount{e.Raw, e.Manufactured, e.Encoded} {
		for _, m := range group {
			if m.Name == "" {
				continue
			}
			rows = append(rows, map[string]any{"itemName": m.Name, "itemCount": m.Count})
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []Event{{Name: EventSetCommanderInventoryMaterials, Timestamp: isoTime(raw.Timestamp), Data: rows}}, nil
}

// mapCargo turns a ship Cargo event into setCommanderInventoryCargo. The
// event is skipped for SRV cargo and when the inventory is written out-of-line
// to Cargo.json (empty inline Inventory).
func mapCargo(raw journal.Raw) ([]Event, error) {
	var e journal.CargoEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if e.Vessel != "" && e.Vessel != "Ship" {
		return nil, nil
	}
	if len(e.Inventory) == 0 {
		return nil, nil
	}
	rows := make([]map[string]any, 0, len(e.Inventory))
	for _, it := range e.Inventory {
		if it.Name == "" {
			continue
		}
		row := map[string]any{"itemName": it.Name, "itemCount": it.Count}
		if it.Stolen > 0 {
			row["isStolen"] = true
		}
		if it.MissionID != 0 {
			row["missionGameID"] = it.MissionID
		}
		rows = append(rows, row)
	}
	return []Event{{Name: EventSetCommanderInventoryCargo, Timestamp: isoTime(raw.Timestamp), Data: rows}}, nil
}

// numField reads a numeric field from a decoded map, tolerating both float64
// and json.Number representations.
func numField(m map[string]any, key string) (float64, bool) {
	switch v := m[key].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

// missionAcceptedEvent is the subset of MissionAccepted we relay.
type missionAcceptedEvent struct {
	journal.Envelope
	Name               string    `json:"Name"`
	MissionID          int64     `json:"MissionID"`
	Faction            string    `json:"Faction"`
	Expiry             time.Time `json:"Expiry"`
	Influence          string    `json:"Influence"`
	Reputation         string    `json:"Reputation"`
	DestinationSystem  string    `json:"DestinationSystem"`
	DestinationStation string    `json:"DestinationStation"`
	TargetFaction      string    `json:"TargetFaction"`
	Commodity          string    `json:"Commodity"`
	Count              int       `json:"Count"`
	KillCount          int       `json:"KillCount"`
	PassengerCount     int       `json:"PassengerCount"`
	PassengerType      string    `json:"PassengerType"`
}

// mapMissionAccepted turns a MissionAccepted event into addCommanderMission,
// stamping the origin system/station from session state (the journal event
// doesn't carry them) — this is what attributes BGS effort to a faction.
func mapMissionAccepted(raw journal.Raw, sess *state.Session) ([]Event, error) {
	var e missionAcceptedEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if e.MissionID == 0 {
		return nil, nil
	}
	data := map[string]any{
		"missionName":   e.Name,
		"missionGameID": strconv.FormatInt(e.MissionID, 10),
	}
	if !e.Expiry.IsZero() {
		data["missionExpiry"] = isoTime(e.Expiry)
	}
	setIfNotEmpty(data, "influenceGain", e.Influence)
	setIfNotEmpty(data, "reputationGain", e.Reputation)
	setIfNotEmpty(data, "minorfactionNameOrigin", e.Faction)
	setIfNotEmpty(data, "starsystemNameTarget", e.DestinationSystem)
	setIfNotEmpty(data, "stationNameTarget", e.DestinationStation)
	setIfNotEmpty(data, "minorfactionNameTarget", e.TargetFaction)
	setIfNotEmpty(data, "commodityName", e.Commodity)
	setIfNotEmpty(data, "passengerType", e.PassengerType)
	if e.Count > 0 {
		data["commodityCount"] = e.Count
	}
	if e.KillCount > 0 {
		data["killCount"] = e.KillCount
	}
	if e.PassengerCount > 0 {
		data["passengerCount"] = e.PassengerCount
	}
	if sys, _ := sess.System(); sys != "" {
		data["starsystemNameOrigin"] = sys
	}
	if docked, station, _ := sess.Dock(); docked && station != "" {
		data["stationNameOrigin"] = station
	}
	return []Event{{Name: EventAddCommanderMission, Timestamp: isoTime(raw.Timestamp), Data: data}}, nil
}

// missionEndEvent covers MissionCompleted/Failed/Abandoned (all keyed by
// MissionID). Completed additionally carries reward/donation credits.
type missionEndEvent struct {
	journal.Envelope
	MissionID int64 `json:"MissionID"`
	Reward    int64 `json:"Reward"`
	Donated   int64 `json:"Donated"`
}

// mapMissionEnd turns MissionCompleted/Failed/Abandoned into the matching
// Inara mission-status event. minorfactionEffects (the per-faction influence
// deltas) are intentionally omitted: the journal carries them in
// FactionEffects, but Inara's expected sub-shape isn't documented precisely
// enough to send safely — the BGS intent is already captured on acceptance
// via addCommanderMission (influenceGain/minorfactionNameOrigin).
func mapMissionEnd(raw journal.Raw, inaraEvent string) ([]Event, error) {
	var e missionEndEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if e.MissionID == 0 {
		return nil, nil
	}
	data := map[string]any{"missionGameID": strconv.FormatInt(e.MissionID, 10)}
	if inaraEvent == EventSetCommanderMissionCompleted {
		if e.Reward > 0 {
			data["rewardCredits"] = e.Reward
		}
		if e.Donated > 0 {
			data["donationCredits"] = e.Donated
		}
	}
	return []Event{{Name: inaraEvent, Timestamp: isoTime(raw.Timestamp), Data: data}}, nil
}

func setIfNotEmpty(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}
