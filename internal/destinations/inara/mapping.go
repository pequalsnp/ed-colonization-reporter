package inara

import (
	"encoding/json"
	"strings"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// fakeStarSystems lists names that appear in the journal but represent
// non-gameplay or off-galaxy contexts. Inara rejects them and the player's
// profile shouldn't be polluted with them.
var fakeStarSystems = map[string]bool{
	"CQC":         true,
	"Training":    true,
	"Destination": true,
}

// mapEvent converts a parsed journal event into zero or more Inara events.
// The shouldSuppressDock flag is consulted (and may be flipped) so that the
// caller can mute a Docked event that the game emits as a side effect of
// CarrierJump or Location-with-Docked. The session supplies context the
// journal event itself lacks (e.g. a mission's origin system/station).
func mapEvent(raw journal.Raw, suppressDock *bool, sess *state.Session) ([]Event, error) {
	switch raw.Event {
	case journal.EventFSDJump:
		return mapFSDJump(raw)
	case journal.EventLocation:
		return mapLocation(raw, suppressDock)
	case journal.EventCarrierJump:
		return mapCarrierJump(raw, suppressDock)
	case journal.EventDocked:
		if *suppressDock {
			*suppressDock = false
			return nil, nil
		}
		return mapDocked(raw)

	// Commander state.
	case journal.EventRank:
		return mapRank(raw)
	case journal.EventProgress:
		return mapProgress(raw)
	case journal.EventReputation:
		return mapReputation(raw)
	case journal.EventLoadGame:
		return mapCredits(raw)
	case journal.EventLoadout:
		return mapLoadout(raw)
	case journal.EventCargo:
		return mapCargo(raw)
	case journal.EventMaterials:
		return mapMaterials(raw)

	// Mission log.
	case journal.EventMissionAccepted:
		return mapMissionAccepted(raw, sess)
	case journal.EventMissionCompleted:
		return mapMissionEnd(raw, EventSetCommanderMissionCompleted)
	case journal.EventMissionFailed:
		return mapMissionEnd(raw, EventSetCommanderMissionFailed)
	case journal.EventMissionAbandoned:
		return mapMissionEnd(raw, EventSetCommanderMissionAbandoned)
	}
	return nil, nil
}

func mapFSDJump(raw journal.Raw) ([]Event, error) {
	var e journal.LocationLikeEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if fakeStarSystems[e.StarSystem] {
		return nil, nil
	}
	locData := map[string]any{
		"starsystemName":   e.StarSystem,
		"starsystemCoords": e.StarPos,
	}
	jumpData := map[string]any{
		"starsystemName":   e.StarSystem,
		"starsystemCoords": e.StarPos,
		"jumpDistance":     extractFloat(raw.Payload, "JumpDist"),
	}
	out := []Event{
		{Name: EventSetCommanderTravelLocation, Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Data: locData},
		{Name: EventAddCommanderTravelFSDJump, Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Data: jumpData},
	}
	// Per-minor-faction standing for this system — the BGS-relevant signal.
	out = append(out, mapMinorFactionReputation(raw)...)
	return out, nil
}

func mapLocation(raw journal.Raw, suppressDock *bool) ([]Event, error) {
	var e struct {
		journal.LocationLikeEvent
		Docked      bool   `json:"Docked"`
		StationName string `json:"StationName"`
		MarketID    int64  `json:"MarketID"`
	}
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if fakeStarSystems[e.StarSystem] {
		return nil, nil
	}
	data := map[string]any{
		"starsystemName":   e.StarSystem,
		"starsystemCoords": e.StarPos,
	}
	if e.Docked {
		data["stationName"] = e.StationName
		if e.MarketID != 0 {
			data["marketID"] = e.MarketID
		}
		// Game emits a Docked event right after Location-with-Docked; we'd
		// otherwise log the dock twice.
		*suppressDock = true
	}
	out := []Event{
		{Name: EventSetCommanderTravelLocation, Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Data: data},
	}
	out = append(out, mapMinorFactionReputation(raw)...)
	return out, nil
}

func mapCarrierJump(raw journal.Raw, suppressDock *bool) ([]Event, error) {
	var e journal.CarrierJumpEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if fakeStarSystems[e.StarSystem] {
		return nil, nil
	}
	loc := map[string]any{
		"starsystemName":   e.StarSystem,
		"starsystemCoords": e.StarPos,
	}
	jump := map[string]any{
		"starsystemName":   e.StarSystem,
		"starsystemCoords": e.StarPos,
		"stationName":      e.StationName,
	}
	if e.MarketID != 0 {
		jump["marketID"] = e.MarketID
	}
	*suppressDock = true
	return []Event{
		{Name: EventSetCommanderTravelLocation, Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Data: loc},
		{Name: EventAddCommanderTravelCarrier, Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Data: jump},
	}, nil
}

func mapDocked(raw journal.Raw) ([]Event, error) {
	var e journal.DockedEvent
	if err := json.Unmarshal(raw.Payload, &e); err != nil {
		return nil, err
	}
	if fakeStarSystems[e.StarSystem] {
		return nil, nil
	}
	data := map[string]any{
		"starsystemName": e.StarSystem,
		"stationName":    e.StationName,
	}
	if e.MarketID != 0 {
		data["marketID"] = e.MarketID
	}
	return []Event{
		{Name: EventAddCommanderTravelDock, Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Data: data},
	}, nil
}

// extractFloat is a tiny utility for plucking a single numeric field out of
// a raw journal payload without re-defining a typed struct each time.
func extractFloat(payload []byte, key string) float64 {
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// isBetaOrLegacy reports whether a gameversion string indicates a galaxy
// Inara won't accept. Inara has refused Legacy uploads since 2022-11-29.
func isBetaOrLegacy(gameVersion string) bool {
	v := strings.ToLower(gameVersion)
	return strings.Contains(v, "beta") || strings.Contains(v, "legacy")
}
