package eddn

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func TestBuildJournalMessage_UnknownEventReturnsNil(t *testing.T) {
	sess := state.New()
	raw := mustRaw(t, "Shutdown", map[string]any{})
	msg, err := buildJournalMessage(raw, sess)
	if err != nil {
		t.Fatal(err)
	}
	if msg != nil {
		t.Errorf("unknown event should map to nil; got %v", msg)
	}
}

func TestBuildJournalMessage_DockedRequiresCachedPos(t *testing.T) {
	sess := state.New()
	// Session has system info but no StarPos (never saw FSDJump/Location).
	sess.SetSystem("Sol", 100)
	raw := mustRaw(t, journal.EventDocked, map[string]any{
		"StarSystem":    "Sol",
		"SystemAddress": 100,
		"StationName":   "X",
		"MarketID":      1,
	})
	_, err := buildJournalMessage(raw, sess)
	if !errors.Is(err, errMissingStarPos) {
		t.Errorf("got %v, want errMissingStarPos", err)
	}
}

func TestBuildJournalMessage_DockedMismatchedSystemDrops(t *testing.T) {
	sess := state.New()
	sess.SetSystemWithPos("Sol", 100, [3]float64{0, 0, 0})
	raw := mustRaw(t, journal.EventDocked, map[string]any{
		"StarSystem":    "Other",
		"SystemAddress": 999, // doesn't match cached 100
		"StationName":   "X",
		"MarketID":      1,
	})
	_, err := buildJournalMessage(raw, sess)
	if !errors.Is(err, errMissingStarPos) {
		t.Errorf("mismatched system must drop with errMissingStarPos; got %v", err)
	}
}

func TestBuildJournalMessage_FSDJumpHasRequiredFields(t *testing.T) {
	sess := state.New()
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Sol",
		"SystemAddress": 100,
		"StarPos":       []any{0.0, 0.0, 0.0},
	})
	msg, err := buildJournalMessage(raw, sess)
	if err != nil {
		t.Fatalf("buildJournalMessage: %v", err)
	}
	for _, k := range []string{"timestamp", "event", "StarSystem", "StarPos", "SystemAddress"} {
		if _, ok := msg[k]; !ok {
			t.Errorf("required field %q missing from message", k)
		}
	}
}

func TestBuildJournalMessage_StripLocalisedRecursively(t *testing.T) {
	sess := state.New()
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Sol",
		"SystemAddress": 100,
		"StarPos":       []any{0.0, 0.0, 0.0},
		"Factions": []any{
			map[string]any{
				"Name":           "X",
				"Name_Localised": "X-loc",
				"States": []any{
					map[string]any{"State": "Boom", "State_Localised": "BOOM!"},
				},
			},
		},
	})
	msg, err := buildJournalMessage(raw, sess)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(msg)
	if containsLocalised(encoded) {
		t.Errorf("output still contains a _Localised key: %s", encoded)
	}
}

func containsLocalised(b []byte) bool {
	// Crude string scan is sufficient — schemas/journal-v1.0.json uses the
	// same approach via patternProperties.
	return len(b) > 0 && (string(b) != "" &&
		(bytesContains(b, []byte("_Localised\""))))
}

func bytesContains(b, sub []byte) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if b[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestBuildJournalMessage_HorizonsOnlyWhenKnown(t *testing.T) {
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Sol",
		"SystemAddress": 100,
		"StarPos":       []any{0.0, 0.0, 0.0},
	})

	// Case 1: DLC flags unknown → omitted from message.
	sess1 := state.New()
	msg1, err := buildJournalMessage(raw, sess1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := msg1["horizons"]; ok {
		t.Error("unknown horizons must not appear in message")
	}
	if _, ok := msg1["odyssey"]; ok {
		t.Error("unknown odyssey must not appear")
	}

	// Case 2: explicitly false → present as false.
	sess2 := state.New()
	no := false
	sess2.SetDLCFlags(&no, &no)
	msg2, err := buildJournalMessage(raw, sess2)
	if err != nil {
		t.Fatal(err)
	}
	if msg2["horizons"] != false || msg2["odyssey"] != false {
		t.Errorf("explicit-false DLC should appear as false; got h=%v o=%v", msg2["horizons"], msg2["odyssey"])
	}
}

func TestBuildCommodityMessage_FiltersNonMarketableAndSkipsEmptyOutput(t *testing.T) {
	sess := state.New()
	// All-NonMarketable inventory should produce no message.
	mf := &journal.MarketFile{
		MarketID: 1, StationName: "X", StarSystem: "Sol", Timestamp: "t",
		Items: []journal.MarketItem{
			{Name: "$drones_name;", Category: "$MARKET_category_NonMarketable;", Stock: 10},
			{Name: "$other_drones_name;", Category: "nonmarketable", Stock: 5},
		},
	}
	if got := buildCommodityMessage(mf, sess); got != nil {
		t.Errorf("all-NonMarketable inventory should produce nil; got %v", got)
	}
}

func TestBuildCommodityMessage_NilInputReturnsNil(t *testing.T) {
	if got := buildCommodityMessage(nil, state.New()); got != nil {
		t.Errorf("nil MarketFile should produce nil message; got %v", got)
	}
}

func TestBuildCommodityMessage_StripsItemIDAndForbiddenFlags(t *testing.T) {
	sess := state.New()
	mf := &journal.MarketFile{
		MarketID: 1, StationName: "X", StarSystem: "Sol", Timestamp: "t",
		Items: []journal.MarketItem{
			{Name: "$titanium_name;", MeanPrice: 100, Stock: 1, Category: ""},
		},
	}
	msg := buildCommodityMessage(mf, sess)
	if msg == nil {
		t.Fatal("expected message")
	}
	commodities := msg["commodities"].([]map[string]any)
	if len(commodities) != 1 {
		t.Fatalf("commodities = %d", len(commodities))
	}
	item := commodities[0]
	for _, forbidden := range []string{"id", "Producer", "Rare", "Category", "Category_Localised", "Name_Localised"} {
		if _, ok := item[forbidden]; ok {
			t.Errorf("forbidden item field %q present", forbidden)
		}
	}
	want := []string{"name", "meanPrice", "buyPrice", "stock", "stockBracket", "sellPrice", "demand", "demandBracket"}
	for _, k := range want {
		if _, ok := item[k]; !ok {
			t.Errorf("required item field %q missing", k)
		}
	}
}

func TestBuildCommodityMessage_RenameTopLevelFields(t *testing.T) {
	sess := state.New()
	mf := &journal.MarketFile{
		MarketID: 42, StationName: "Y", StationType: "FleetCarrier",
		StarSystem: "Sol", Timestamp: "ts",
		Items: []journal.MarketItem{{Name: "$x_name;", Stock: 1, MeanPrice: 10}},
	}
	msg := buildCommodityMessage(mf, sess)
	want := map[string]any{
		"systemName":  "Sol",
		"stationName": "Y",
		"marketId":    int64(42),
		"timestamp":   "ts",
		"stationType": "FleetCarrier",
	}
	for k, v := range want {
		if got, ok := msg[k]; !ok || !reflect.DeepEqual(got, v) {
			t.Errorf("msg[%q] = %v, want %v", k, got, v)
		}
	}
	// Underlying journal field names must NOT leak through.
	for _, forbidden := range []string{"StarSystem", "StationName", "MarketID", "StationType"} {
		if _, ok := msg[forbidden]; ok {
			t.Errorf("legacy field %q must not appear in renamed message", forbidden)
		}
	}
}
