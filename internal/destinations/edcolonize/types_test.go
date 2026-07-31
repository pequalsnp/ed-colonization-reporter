package edcolonize

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The types in this package mirror edcolonize's internal/cmdr. They are in a
// different module and can't be imported, so nothing but a test stops a
// rename here from silently breaking the channel — the receiver would reject
// the snapshot with 400 and the only symptom would be missing state.
//
// This pins every json tag the receiver reads. If you change one, change
// edcolonize's internal/cmdr/types.go in the same change, bump SchemaVersion
// on both sides, and update this test.
func TestWireTagsMatchReceiver(t *testing.T) {
	want := map[string][]string{
		"Snapshot": {
			"schema_version", "captured_at", "commander", "fid", "game_version",
			"game_build", "horizons", "odyssey", "location", "ship", "credits",
			"ranks", "cargo", "materials", "missions", "colonisation", "carriers",
		},
		"Location": {
			"star_system", "system_address", "star_pos", "body", "docked",
			"station_name", "station_type", "market_id",
		},
		"Ship": {
			"type", "name", "ident", "hull_value", "rebuy", "cargo_capacity",
			"fuel_capacity", "fuel_level", "max_jump_range", "modules",
		},
		"Module":        {"slot", "item", "on", "blueprint", "grade", "experimental_effect"},
		"CargoItem":     {"name", "localised_name", "count", "stolen"},
		"Materials":     {"raw", "manufactured", "encoded"},
		"MaterialItem":  {"name", "localised_name", "count"},
		"DepotResource": {"name", "localised_name", "required", "provided", "payment"},
		"Mission": {
			"mission_id", "name", "localised_name", "faction", "destination_system",
			"destination_station", "commodity", "count", "reward", "expiry", "accepted_at",
		},
		"Depot": {
			"market_id", "star_system", "system_address", "station_name",
			"construction_progress", "construction_complete", "construction_failed",
			"resources", "updated_at",
		},
		"Ranks": {
			"combat", "trade", "explore", "soldier", "exobiologist", "cqc",
			"empire", "federation", "combat_progress", "trade_progress",
			"explore_progress", "empire_progress", "federation_progress",
		},
	}

	types := map[string]reflect.Type{
		"Snapshot":      reflect.TypeFor[Snapshot](),
		"Location":      reflect.TypeFor[Location](),
		"Ship":          reflect.TypeFor[Ship](),
		"Module":        reflect.TypeFor[Module](),
		"CargoItem":     reflect.TypeFor[CargoItem](),
		"Materials":     reflect.TypeFor[Materials](),
		"MaterialItem":  reflect.TypeFor[MaterialItem](),
		"DepotResource": reflect.TypeFor[DepotResource](),
		"Mission":       reflect.TypeFor[Mission](),
		"Depot":         reflect.TypeFor[Depot](),
		"Ranks":         reflect.TypeFor[Ranks](),
	}

	for name, typ := range types {
		got := make([]string, 0, typ.NumField())
		for field := range typ.Fields() {
			tag := field.Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			got = append(got, strings.Split(tag, ",")[0])
		}
		if !reflect.DeepEqual(got, want[name]) {
			t.Errorf("%s wire tags drifted:\n got: %v\nwant: %v", name, got, want[name])
		}
	}
}

// The receiver validates that these are present; a snapshot missing them is
// rejected outright, so they must never gain omitempty.
func TestRequiredFieldsAreNeverOmitted(t *testing.T) {
	b, err := json.Marshal(Snapshot{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"schema_version", "captured_at", "commander", "location"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("%q must always serialise (receiver validates it), got %s", key, b)
		}
	}
}

// The receiver uses DisallowUnknownFields, so an optional section that
// serialises as null rather than being omitted is fine, but a *new* field
// added here without the receiver knowing it would 400 the whole snapshot.
// This documents that coupling with a concrete round-trip.
func TestSnapshotRoundTrips(t *testing.T) {
	credits := int64(999)
	in := Snapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    time.Unix(1700000000, 0).UTC(),
		Commander:     "Kyle",
		Location:      Location{StarSystem: "Sol", Docked: true, MarketID: 128},
		Credits:       &credits,
		Colonisation: []Depot{{
			MarketID:  128,
			Resources: []DepotResource{{Name: "steel", Required: 500, Provided: 160}},
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip changed the snapshot:\n in: %+v\nout: %+v", in, out)
	}
}
