package edcolonize

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// buildAfter feeds events through the uploader and returns the snapshot it
// would send, without any network involved.
func buildAfter(t *testing.T, sess *state.Session, events ...func(*testing.T) map[string]any) *Snapshot {
	t.Helper()
	u := New(SoftwareID{}, Config{Enabled: true, URL: "http://example.invalid", Token: "t"}, sess)
	defer u.Close()
	for _, mk := range events {
		payload := mk(t)
		event, _ := payload["__event"].(string)
		delete(payload, "__event")
		if err := u.HandleEvent(context.Background(), raw(t, event, payload)); err != nil {
			t.Fatalf("HandleEvent(%s): %v", event, err)
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.buildLocked()
}

func ev(event string, fields map[string]any) func(*testing.T) map[string]any {
	return func(*testing.T) map[string]any {
		out := map[string]any{"__event": event}
		for k, v := range fields {
			out[k] = v
		}
		return out
	}
}

func TestMaterialsAreCapturedAndSorted(t *testing.T) {
	snap := buildAfter(t, newTestSession(), ev("Materials", map[string]any{
		"Raw": []map[string]any{
			{"Name": "zinc", "Count": 40},
			{"Name": "iron", "Name_Localised": "Iron", "Count": 217},
		},
		"Manufactured": []map[string]any{
			{"Name": "shieldemitters", "Name_Localised": "Shield Emitters", "Count": 12},
		},
		"Encoded": []map[string]any{},
	}))

	if snap.Materials == nil {
		t.Fatal("materials section missing")
	}
	if len(snap.Materials.Raw) != 2 {
		t.Fatalf("raw materials = %d, want 2", len(snap.Materials.Raw))
	}
	// Sorted by internal name for stable diffs.
	if snap.Materials.Raw[0].Name != "iron" || snap.Materials.Raw[1].Name != "zinc" {
		t.Errorf("raw materials not sorted: %+v", snap.Materials.Raw)
	}
	if snap.Materials.Raw[0].Count != 217 {
		t.Errorf("iron count = %d, want 217", snap.Materials.Raw[0].Count)
	}
	if len(snap.Materials.Encoded) != 0 {
		t.Errorf("empty encoded list should stay empty, got %+v", snap.Materials.Encoded)
	}
}

func TestMissionLifecycle(t *testing.T) {
	sess := newTestSession()

	t.Run("accepted missions appear", func(t *testing.T) {
		snap := buildAfter(t, sess,
			ev("MissionAccepted", map[string]any{
				"MissionID": 900001, "Name": "Mission_Delivery",
				"LocalisedName": "Deliver Steel", "Faction": "Sol Workers",
				"DestinationSystem": "Alpha Centauri", "DestinationStation": "Hutton Orbital",
				"Commodity": "$Steel_Name;", "Commodity_Localised": "Steel",
				"Count": 340, "Reward": 1200000,
			}),
		)
		if len(snap.Missions) != 1 {
			t.Fatalf("missions = %d, want 1", len(snap.Missions))
		}
		m := snap.Missions[0]
		if m.MissionID != 900001 || m.Count != 340 {
			t.Errorf("unexpected mission: %+v", m)
		}
		// Localised commodity is preferred over the internal symbol.
		if m.Commodity != "Steel" {
			t.Errorf("commodity = %q, want Steel", m.Commodity)
		}
		if m.DestinationSystem != "Alpha Centauri" {
			t.Errorf("destination = %q", m.DestinationSystem)
		}
	})

	t.Run("completed missions are removed", func(t *testing.T) {
		snap := buildAfter(t, sess,
			ev("MissionAccepted", map[string]any{"MissionID": 1, "Name": "a"}),
			ev("MissionAccepted", map[string]any{"MissionID": 2, "Name": "b"}),
			ev("MissionCompleted", map[string]any{"MissionID": 1}),
		)
		if len(snap.Missions) != 1 || snap.Missions[0].MissionID != 2 {
			t.Errorf("after completing 1, missions = %+v", snap.Missions)
		}
	})

	t.Run("abandoned and failed are removed too", func(t *testing.T) {
		snap := buildAfter(t, sess,
			ev("MissionAccepted", map[string]any{"MissionID": 1}),
			ev("MissionAccepted", map[string]any{"MissionID": 2}),
			ev("MissionAbandoned", map[string]any{"MissionID": 1}),
			ev("MissionFailed", map[string]any{"MissionID": 2}),
		)
		if len(snap.Missions) != 0 {
			t.Errorf("missions = %+v, want empty", snap.Missions)
		}
	})

	// The session-start Missions event is authoritative. Anything we think is
	// active but isn't must be dropped, or a mission resolved while the
	// reporter was closed would linger forever.
	t.Run("session-start list prunes stale missions", func(t *testing.T) {
		snap := buildAfter(t, sess,
			ev("MissionAccepted", map[string]any{"MissionID": 111}),
			ev("MissionAccepted", map[string]any{"MissionID": 222}),
			ev("Missions", map[string]any{
				"Active": []map[string]any{{"MissionID": 222, "Name": "still going"}},
			}),
		)
		if len(snap.Missions) != 1 || snap.Missions[0].MissionID != 222 {
			t.Errorf("missions = %+v, want only 222", snap.Missions)
		}
	})

	t.Run("session-start list adds unknown missions", func(t *testing.T) {
		snap := buildAfter(t, sess, ev("Missions", map[string]any{
			"Active": []map[string]any{{"MissionID": 777, "Name": "from a previous session"}},
		}))
		if len(snap.Missions) != 1 || snap.Missions[0].MissionID != 777 {
			t.Errorf("missions = %+v, want 777 present", snap.Missions)
		}
	})
}

func TestColonisationDepotCapturedAndEnriched(t *testing.T) {
	sess := newTestSession()
	sess.SetDocked("Hutton Orbital", 3223343616, 10477373803)

	snap := buildAfter(t, sess, ev("ColonisationConstructionDepot", map[string]any{
		"MarketID":             3223343616,
		"ConstructionProgress": 0.42,
		"ResourcesRequired": []map[string]any{
			{"Name": "$steel_name;", "Name_Localised": "Steel",
				"RequiredAmount": 500, "ProvidedAmount": 160, "Payment": 3200},
			{"Name": "$titanium_name;", "Name_Localised": "Titanium",
				"RequiredAmount": 200, "ProvidedAmount": 200, "Payment": 1100},
		},
	}))

	if len(snap.Colonisation) != 1 {
		t.Fatalf("depots = %d, want 1", len(snap.Colonisation))
	}
	d := snap.Colonisation[0]
	if d.ConstructionProgress != 0.42 {
		t.Errorf("progress = %v, want 0.42", d.ConstructionProgress)
	}
	// The depot event carries only a MarketID; system/station come from session.
	if d.StarSystem != "Sol" {
		t.Errorf("star_system = %q, want Sol (enriched from session)", d.StarSystem)
	}
	if d.StationName != "Hutton Orbital" {
		t.Errorf("station_name = %q, want Hutton Orbital", d.StationName)
	}
	if len(d.Resources) != 2 {
		t.Fatalf("resources = %d, want 2", len(d.Resources))
	}
	// Internal symbols are unwrapped from the $name; form.
	if d.Resources[0].Name != "steel_name" {
		t.Errorf("resource name = %q, want steel_name", d.Resources[0].Name)
	}
	if d.Resources[0].Required != 500 || d.Resources[0].Provided != 160 {
		t.Errorf("unexpected resource amounts: %+v", d.Resources[0])
	}
}

func TestDepotUpdatesReplacePriorState(t *testing.T) {
	sess := newTestSession()
	snap := buildAfter(t, sess,
		ev("ColonisationConstructionDepot", map[string]any{
			"MarketID": 42, "ConstructionProgress": 0.1,
			"ResourcesRequired": []map[string]any{
				{"Name": "$steel_name;", "RequiredAmount": 500, "ProvidedAmount": 0},
			},
		}),
		ev("ColonisationConstructionDepot", map[string]any{
			"MarketID": 42, "ConstructionProgress": 0.8,
			"ResourcesRequired": []map[string]any{
				{"Name": "$steel_name;", "RequiredAmount": 500, "ProvidedAmount": 400},
			},
		}),
	)
	if len(snap.Colonisation) != 1 {
		t.Fatalf("depots = %d, want 1 (same MarketID must not duplicate)", len(snap.Colonisation))
	}
	if snap.Colonisation[0].ConstructionProgress != 0.8 {
		t.Errorf("progress = %v, want the later 0.8", snap.Colonisation[0].ConstructionProgress)
	}
	if got := snap.Colonisation[0].Resources[0].Provided; got != 400 {
		t.Errorf("provided = %d, want 400", got)
	}
}

func TestCreditsAndRanks(t *testing.T) {
	snap := buildAfter(t, newTestSession(),
		ev("LoadGame", map[string]any{"Commander": "Kyle", "Credits": 4200000000}),
		ev("Rank", map[string]any{"Combat": 5, "Trade": 8, "Explore": 7, "Empire": 12}),
		ev("Progress", map[string]any{"Combat": 43, "Trade": 91}),
	)
	if snap.Credits == nil || *snap.Credits != 4200000000 {
		t.Errorf("credits = %v, want 4200000000", snap.Credits)
	}
	if snap.Ranks == nil {
		t.Fatal("ranks missing")
	}
	if snap.Ranks.Trade != 8 || snap.Ranks.Empire != 12 {
		t.Errorf("ranks = %+v", snap.Ranks)
	}
	// Rank and Progress share field names but mean different things; they
	// must land in separate fields rather than overwriting each other.
	if snap.Ranks.TradeProgress != 91 {
		t.Errorf("trade progress = %d, want 91", snap.Ranks.TradeProgress)
	}
	if snap.Ranks.Trade != 8 {
		t.Errorf("Progress overwrote the Trade rank: %d", snap.Ranks.Trade)
	}
}

func TestLoadoutSummarisedFromSession(t *testing.T) {
	sess := newTestSession()
	loadout := map[string]any{
		"event": "Loadout", "Ship": "python", "ShipName": "Hauler One",
		"ShipIdent": "KG-01", "CargoCapacity": 256, "MaxJumpRange": 34.2,
		"HullValue":    int64(55000000),
		"FuelCapacity": map[string]any{"Main": 32.0, "Reserve": 0.83},
		"Modules": []map[string]any{
			{"Slot": "FrameShiftDrive", "Item": "int_hyperdrive_size5_class5", "On": true,
				"Engineering": map[string]any{
					"BlueprintName": "FSD_LongRange", "Level": 5,
					"ExperimentalEffect_Localised": "Mass Manager",
				}},
			{"Slot": "PowerPlant", "Item": "int_powerplant_size6_class5", "On": true},
		},
	}
	b, err := json.Marshal(loadout)
	if err != nil {
		t.Fatalf("marshal loadout: %v", err)
	}
	sess.SetShipLoadout(b, "python", "Hauler One", "KG-01")

	snap := buildAfter(t, sess, ev("Loadout", map[string]any{}))
	if snap.Ship == nil {
		t.Fatal("ship section missing")
	}
	if snap.Ship.Type != "python" || snap.Ship.Name != "Hauler One" {
		t.Errorf("ship = %+v", snap.Ship)
	}
	if snap.Ship.CargoCapacity == nil || *snap.Ship.CargoCapacity != 256 {
		t.Errorf("cargo capacity = %v, want 256", snap.Ship.CargoCapacity)
	}
	if snap.Ship.FuelCapacity == nil || *snap.Ship.FuelCapacity != 32.0 {
		t.Errorf("fuel capacity = %v, want 32", snap.Ship.FuelCapacity)
	}
	if len(snap.Ship.Modules) != 2 {
		t.Fatalf("modules = %d, want 2", len(snap.Ship.Modules))
	}
	fsd := snap.Ship.Modules[0]
	if fsd.Blueprint != "FSD_LongRange" || fsd.Grade != 5 {
		t.Errorf("engineering not summarised: %+v", fsd)
	}
	if fsd.Experiment != "Mass Manager" {
		t.Errorf("experimental effect = %q", fsd.Experiment)
	}
	// An un-engineered module must not invent engineering fields.
	if snap.Ship.Modules[1].Blueprint != "" || snap.Ship.Modules[1].Grade != 0 {
		t.Errorf("un-engineered module carries engineering: %+v", snap.Ship.Modules[1])
	}
}

func TestCapturedAtIsBuildTimeNotEventTime(t *testing.T) {
	// A replayed journal carries old event timestamps. captured_at must be
	// the moment we assembled the picture, or every replay would look like
	// enormous clock skew to the receiver.
	before := time.Now().UTC()
	snap := buildAfter(t, newTestSession(), ev("FSDJump", map[string]any{}))
	if snap.CapturedAt.Before(before.Add(-time.Second)) {
		t.Errorf("captured_at = %v, want ~now (%v)", snap.CapturedAt, before)
	}
}

func TestUninterestingEventsDoNotMarkDirty(t *testing.T) {
	u := New(SoftwareID{}, Config{Enabled: true, URL: "http://example.invalid", Token: "t"}, newTestSession())
	defer u.Close()

	if err := u.HandleEvent(context.Background(), raw(t, "ReceiveText", map[string]any{})); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	u.mu.Lock()
	dirty := u.dirty
	u.mu.Unlock()
	if dirty {
		t.Error("an irrelevant event marked the snapshot dirty; that defeats the debounce")
	}
}
