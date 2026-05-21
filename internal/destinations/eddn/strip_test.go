package eddn

import (
	"reflect"
	"testing"
)

func TestStripLocalised_RemovesAtEveryDepth(t *testing.T) {
	in := map[string]any{
		"Name":           "$titanium_name;",
		"Name_Localised": "Titanium",
		"Factions": []any{
			map[string]any{
				"Name":           "Sol Workers Party",
				"Name_Localised": "Sol Workers Party (translated)",
				"Government":     "$government_democracy;",
				"Government_Localised": "Democracy",
			},
		},
	}
	stripLocalised(in)

	if _, ok := in["Name_Localised"]; ok {
		t.Error("top-level _Localised not removed")
	}
	f := in["Factions"].([]any)[0].(map[string]any)
	if _, ok := f["Name_Localised"]; ok {
		t.Error("nested _Localised in faction not removed")
	}
	if _, ok := f["Government_Localised"]; ok {
		t.Error("second nested _Localised not removed")
	}
	if in["Name"] != "$titanium_name;" {
		t.Error("non-localised keys must be untouched")
	}
	if f["Name"] != "Sol Workers Party" {
		t.Error("nested non-localised must be untouched")
	}
}

func TestStripJournalForbidden_TopLevel(t *testing.T) {
	in := map[string]any{
		"event":         "FSDJump",
		"StarSystem":    "Sol",
		"FuelLevel":     32.0,
		"FuelUsed":      8.0,
		"JumpDist":      14.3,
		"BoostUsed":     0,
		"Wanted":        true,
		"CockpitBreach": false,
	}
	stripJournalForbidden(in)
	want := map[string]any{
		"event":      "FSDJump",
		"StarSystem": "Sol",
	}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("after strip = %+v, want %+v", in, want)
	}
}

func TestStripJournalForbidden_FactionLevel(t *testing.T) {
	in := map[string]any{
		"event":      "FSDJump",
		"StarSystem": "Sol",
		"Factions": []any{
			map[string]any{
				"Name":             "Faction A",
				"HappiestSystem":   "Foo",
				"HomeSystem":       "Bar",
				"MyReputation":     50.0,
				"SquadronFaction":  true,
				"FactionState":     "Boom",
			},
		},
	}
	stripJournalForbidden(in)
	f := in["Factions"].([]any)[0].(map[string]any)
	for _, forbidden := range []string{"HappiestSystem", "HomeSystem", "MyReputation", "SquadronFaction"} {
		if _, ok := f[forbidden]; ok {
			t.Errorf("faction key %q not stripped", forbidden)
		}
	}
	if f["Name"] != "Faction A" || f["FactionState"] != "Boom" {
		t.Errorf("non-forbidden faction keys lost: %+v", f)
	}
}

func TestStripJournalForbidden_LeavesUnrelatedAlone(t *testing.T) {
	in := map[string]any{
		"StarSystem":    "Sol",
		"StarPos":       []any{0.0, 0.0, 0.0},
		"SystemAddress": 10477373803,
	}
	stripJournalForbidden(in)
	if len(in) != 3 {
		t.Errorf("strip removed non-forbidden keys: %+v", in)
	}
}
