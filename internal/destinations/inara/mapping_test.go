package inara

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func raw(t *testing.T, event string, payload map[string]any) journal.Raw {
	t.Helper()
	payload["event"] = event
	if _, ok := payload["timestamp"]; !ok {
		payload["timestamp"] = "2026-05-21T12:00:00Z"
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r, err := journal.ParseLine(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMapFSDJump_EmitsLocationAndJump(t *testing.T) {
	r := raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Alpha Centauri",
		"SystemAddress": 1234567,
		"StarPos":       []any{0.5, 1.5, 2.5},
		"JumpDist":      14.3,
	})
	suppress := false
	events, err := mapEvent(r, &suppress, state.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Name != EventSetCommanderTravelLocation {
		t.Errorf("first event name = %q", events[0].Name)
	}
	if events[1].Name != EventAddCommanderTravelFSDJump {
		t.Errorf("second event name = %q", events[1].Name)
	}
	jump := events[1].Data.(map[string]any)
	if jump["jumpDistance"].(float64) != 14.3 {
		t.Errorf("jumpDistance = %v", jump["jumpDistance"])
	}
}

func TestMapFSDJump_SkipsFakeSystems(t *testing.T) {
	for _, sys := range []string{"CQC", "Training", "Destination"} {
		r := raw(t, journal.EventFSDJump, map[string]any{
			"StarSystem": sys, "SystemAddress": 1, "StarPos": []any{0, 0, 0},
		})
		suppress := false
		events, err := mapEvent(r, &suppress, state.New())
		if err != nil {
			t.Fatalf("%s: %v", sys, err)
		}
		if len(events) != 0 {
			t.Errorf("%s should be skipped; got %d events", sys, len(events))
		}
	}
}

func TestMapLocation_DockedSetsSuppressFlag(t *testing.T) {
	r := raw(t, journal.EventLocation, map[string]any{
		"StarSystem":  "Sol",
		"StarPos":     []any{0, 0, 0},
		"Docked":      true,
		"StationName": "Abraham Lincoln",
		"MarketID":    128666761,
	})
	suppress := false
	events, err := mapEvent(r, &suppress, state.New())
	if err != nil {
		t.Fatal(err)
	}
	if !suppress {
		t.Error("Location-with-Docked must set suppressDock = true")
	}
	if len(events) != 1 || events[0].Name != EventSetCommanderTravelLocation {
		t.Errorf("expected one setCommanderTravelLocation; got %d events", len(events))
	}
	data := events[0].Data.(map[string]any)
	if data["stationName"] != "Abraham Lincoln" {
		t.Errorf("stationName = %v", data["stationName"])
	}
}

func TestMapDocked_SuppressedAfterCarrierJump(t *testing.T) {
	// Simulate the sequence: CarrierJump -> Docked. The Docked must be suppressed.
	carrierRaw := raw(t, journal.EventCarrierJump, map[string]any{
		"Docked":        true,
		"StationName":   "MY-FC ABC-12X",
		"StationType":   "FleetCarrier",
		"MarketID":      3700000123,
		"StarSystem":    "Sol",
		"SystemAddress": 100,
		"StarPos":       []any{0, 0, 0},
	})
	suppress := false
	cevents, err := mapEvent(carrierRaw, &suppress, state.New())
	if err != nil {
		t.Fatal(err)
	}
	if !suppress {
		t.Fatal("CarrierJump should set suppressDock = true")
	}
	if len(cevents) != 2 {
		t.Errorf("CarrierJump should emit 2 events; got %d", len(cevents))
	}

	// Now the Docked event arrives; should be suppressed and clear the flag.
	dockedRaw := raw(t, journal.EventDocked, map[string]any{
		"StarSystem": "Sol", "StationName": "MY-FC ABC-12X", "MarketID": 3700000123,
	})
	devents, err := mapEvent(dockedRaw, &suppress, state.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(devents) != 0 {
		t.Errorf("suppressed Docked should emit 0 events; got %d", len(devents))
	}
	if suppress {
		t.Error("suppressDock should be cleared after the suppressed Docked")
	}
}

func TestMapDocked_NormalCase(t *testing.T) {
	r := raw(t, journal.EventDocked, map[string]any{
		"StarSystem": "Sol", "StationName": "Abraham Lincoln", "MarketID": 128666761,
	})
	suppress := false
	events, err := mapEvent(r, &suppress, state.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Name != EventAddCommanderTravelDock {
		t.Errorf("expected 1 addCommanderTravelDock; got %d events", len(events))
	}
	data := events[0].Data.(map[string]any)
	want := map[string]any{
		"starsystemName": "Sol",
		"stationName":    "Abraham Lincoln",
		"marketID":       int64(128666761),
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("dock data = %+v, want %+v", data, want)
	}
}

func TestMapEvent_UnknownEventReturnsNil(t *testing.T) {
	r := raw(t, "SomethingElse", map[string]any{"foo": "bar"})
	suppress := false
	events, err := mapEvent(r, &suppress, state.New())
	if err != nil {
		t.Fatal(err)
	}
	if events != nil {
		t.Errorf("unknown event should map to nil; got %v", events)
	}
}

func TestIsBetaOrLegacy(t *testing.T) {
	cases := map[string]bool{
		"4.0.0.1903":         false,
		"4.0.0.beta1":        true,
		"3.8.0-legacy":       true,
		"":                   false,
		"4.0.0 Live":         false,
	}
	for in, want := range cases {
		if got := isBetaOrLegacy(in); got != want {
			t.Errorf("isBetaOrLegacy(%q) = %v, want %v", in, got, want)
		}
	}
}
