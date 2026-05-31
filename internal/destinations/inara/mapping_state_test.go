package inara

import (
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// findEvent returns the first event with the given name, or fails.
func findEvent(t *testing.T, events []Event, name string) Event {
	t.Helper()
	for _, e := range events {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no %s event in %v", name, eventNames(events))
	return Event{}
}

func eventNames(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Name
	}
	return out
}

func mapOne(t *testing.T, r journal.Raw, sess *state.Session) []Event {
	t.Helper()
	suppress := false
	events, err := mapEvent(r, &suppress, sess)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestMapRankAndProgress(t *testing.T) {
	rankEv := mapOne(t, raw(t, journal.EventRank, map[string]any{
		"Combat": 3, "Trade": 5, "Explore": 8, "Federation": 4, "Empire": 0,
	}), state.New())
	e := findEvent(t, rankEv, EventSetCommanderRankPilot)
	rows := e.Data.([]map[string]any)
	if len(rows) != 5 {
		t.Fatalf("rank rows = %d, want 5", len(rows))
	}
	// Spot-check the explore rank value.
	found := false
	for _, r := range rows {
		if r["rankName"] == "explore" {
			if r["rankValue"] != 8 {
				t.Errorf("explore rankValue = %v, want 8", r["rankValue"])
			}
			found = true
		}
	}
	if !found {
		t.Error("explore rank missing")
	}

	progEv := mapOne(t, raw(t, journal.EventProgress, map[string]any{"Combat": 50}), state.New())
	pe := findEvent(t, progEv, EventSetCommanderRankPilot)
	prows := pe.Data.([]map[string]any)
	if prows[0]["rankProgress"] != 0.5 {
		t.Errorf("Combat progress = %v, want 0.5", prows[0]["rankProgress"])
	}
}

func TestMapReputationMajor(t *testing.T) {
	ev := mapOne(t, raw(t, journal.EventReputation, map[string]any{
		"Federation": 75.0, "Empire": -20.0, "Alliance": 0.0,
	}), state.New())
	e := findEvent(t, ev, EventSetCommanderReputationMajorFaction)
	rows := e.Data.([]map[string]any)
	byName := map[string]float64{}
	for _, r := range rows {
		byName[r["majorfactionName"].(string)] = r["majorfactionReputation"].(float64)
	}
	if byName["federation"] != 0.75 {
		t.Errorf("federation rep = %v, want 0.75", byName["federation"])
	}
	if byName["empire"] != -0.20 {
		t.Errorf("empire rep = %v, want -0.20", byName["empire"])
	}
}

func TestMapMinorFactionReputation_OnFSDJump(t *testing.T) {
	ev := mapOne(t, raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Sol",
		"SystemAddress": 1,
		"StarPos":       []any{0, 0, 0},
		"Factions": []any{
			map[string]any{"Name": "Sol Workers", "MyReputation": 35.0},
			map[string]any{"Name": "Mother Gaia", "MyReputation": 0.0}, // skipped (no standing)
		},
	}), state.New())
	e := findEvent(t, ev, EventSetCommanderReputationMinorFaction)
	rows := e.Data.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("minor faction rows = %d, want 1 (zero-rep skipped)", len(rows))
	}
	if rows[0]["minorfactionName"] != "Sol Workers" || rows[0]["minorfactionReputation"] != 0.35 {
		t.Errorf("minor faction row wrong: %v", rows[0])
	}
}

func TestMapMinorFactionReputation_OnLocation(t *testing.T) {
	ev := mapOne(t, raw(t, journal.EventLocation, map[string]any{
		"StarSystem":    "Sol",
		"SystemAddress": 1,
		"StarPos":       []any{0, 0, 0},
		"Factions": []any{
			map[string]any{"Name": "Sol Workers", "MyReputation": -10.0},
		},
	}), state.New())
	e := findEvent(t, ev, EventSetCommanderReputationMinorFaction)
	rows := e.Data.([]map[string]any)
	if rows[0]["minorfactionReputation"] != -0.10 {
		t.Errorf("rep = %v, want -0.10", rows[0]["minorfactionReputation"])
	}
}

func TestMapCredits(t *testing.T) {
	ev := mapOne(t, raw(t, journal.EventLoadGame, map[string]any{
		"Commander": "Jameson", "Credits": 123456789, "Loan": 0,
	}), state.New())
	e := findEvent(t, ev, EventSetCommanderCredits)
	d := e.Data.(map[string]any)
	if d["commanderCredits"].(int64) != 123456789 {
		t.Errorf("credits = %v", d["commanderCredits"])
	}
}

func TestMapLoadout(t *testing.T) {
	ev := mapOne(t, raw(t, journal.EventLoadout, map[string]any{
		"Ship": "anaconda", "ShipID": 7, "ShipName": "Voyager", "ShipIdent": "VY-1",
		"HullValue": 1000000, "ModulesValue": 5000000, "Rebuy": 300000,
		"MaxJumpRange": 45.6, "CargoCapacity": 256,
	}), state.New())
	e := findEvent(t, ev, EventSetCommanderShip)
	d := e.Data.(map[string]any)
	if d["shipType"] != "anaconda" || d["shipGameID"].(int64) != 7 {
		t.Errorf("ship identity wrong: %v", d)
	}
	if d["isCurrentShip"] != true {
		t.Error("isCurrentShip should be true")
	}
	if d["shipMaxJumpRange"] != 45.6 || d["shipCargoCapacity"] != 256 {
		t.Errorf("ship stats wrong: %v", d)
	}
}

func TestMapCargo_ShipOnlyWithInlineInventory(t *testing.T) {
	// SRV cargo skipped.
	if ev := mapOne(t, raw(t, journal.EventCargo, map[string]any{
		"Vessel": "SRV", "Count": 1,
		"Inventory": []any{map[string]any{"Name": "limpet", "Count": 1}},
	}), state.New()); len(ev) != 0 {
		t.Errorf("SRV cargo should be skipped, got %v", eventNames(ev))
	}
	// Empty inline inventory (written to Cargo.json) skipped.
	if ev := mapOne(t, raw(t, journal.EventCargo, map[string]any{"Vessel": "Ship", "Count": 5}), state.New()); len(ev) != 0 {
		t.Errorf("empty inline cargo should be skipped, got %v", eventNames(ev))
	}
	// Ship cargo with inline inventory → setCommanderInventoryCargo.
	ev := mapOne(t, raw(t, journal.EventCargo, map[string]any{
		"Vessel": "Ship", "Count": 2,
		"Inventory": []any{
			map[string]any{"Name": "gold", "Count": 2, "Stolen": 1},
		},
	}), state.New())
	e := findEvent(t, ev, EventSetCommanderInventoryCargo)
	rows := e.Data.([]map[string]any)
	if rows[0]["itemName"] != "gold" || rows[0]["isStolen"] != true {
		t.Errorf("cargo row wrong: %v", rows[0])
	}
}

func TestMapMissionAccepted_StampsOrigin(t *testing.T) {
	sess := state.New()
	sess.SetSystem("Sol", 1)
	sess.SetDocked("Abraham Lincoln", 128, 1)
	ev := mapOne(t, raw(t, journal.EventMissionAccepted, map[string]any{
		"Name":              "Mission_Delivery",
		"MissionID":         9001,
		"Faction":           "Sol Workers",
		"Influence":         "++",
		"Reputation":        "+",
		"DestinationSystem": "Wolf 359",
		"Commodity":         "$Gold_Name;",
		"Count":             20,
	}), sess)
	e := findEvent(t, ev, EventAddCommanderMission)
	d := e.Data.(map[string]any)
	if d["missionGameID"] != "9001" {
		t.Errorf("missionGameID = %v, want \"9001\"", d["missionGameID"])
	}
	if d["minorfactionNameOrigin"] != "Sol Workers" || d["influenceGain"] != "++" {
		t.Errorf("BGS fields wrong: %v", d)
	}
	if d["starsystemNameOrigin"] != "Sol" || d["stationNameOrigin"] != "Abraham Lincoln" {
		t.Errorf("origin not stamped from session: %v", d)
	}
	if d["starsystemNameTarget"] != "Wolf 359" || d["commodityCount"] != 20 {
		t.Errorf("target/commodity wrong: %v", d)
	}
}

func TestMapMissionEnd(t *testing.T) {
	comp := mapOne(t, raw(t, journal.EventMissionCompleted, map[string]any{
		"MissionID": 9001, "Reward": 50000, "Donated": 0,
	}), state.New())
	e := findEvent(t, comp, EventSetCommanderMissionCompleted)
	d := e.Data.(map[string]any)
	if d["missionGameID"] != "9001" || d["rewardCredits"].(int64) != 50000 {
		t.Errorf("completed wrong: %v", d)
	}

	failed := mapOne(t, raw(t, journal.EventMissionFailed, map[string]any{"MissionID": 9001}), state.New())
	fe := findEvent(t, failed, EventSetCommanderMissionFailed)
	if fe.Data.(map[string]any)["missionGameID"] != "9001" {
		t.Errorf("failed missionGameID wrong: %v", fe.Data)
	}

	ab := mapOne(t, raw(t, journal.EventMissionAbandoned, map[string]any{"MissionID": 9001}), state.New())
	findEvent(t, ab, EventSetCommanderMissionAbandoned)
}
