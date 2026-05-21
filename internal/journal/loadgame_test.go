package journal

import (
	"encoding/json"
	"testing"
)

func TestParseFileheader(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T12:00:00Z","event":"Fileheader","part":1,"language":"English/UK","Odyssey":true,"gameversion":"4.0.0.1903","build":"r12345/r0 "}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Event != EventFileheader {
		t.Errorf("event = %q", raw.Event)
	}
	var fh FileheaderEvent
	if err := json.Unmarshal(raw.Payload, &fh); err != nil {
		t.Fatal(err)
	}
	if fh.GameVersion != "4.0.0.1903" {
		t.Errorf("GameVersion = %q", fh.GameVersion)
	}
	if fh.GameBuild != "r12345/r0 " {
		t.Errorf("GameBuild = %q (trailing whitespace must be preserved)", fh.GameBuild)
	}
}

func TestParseLoadGame_WithDLCFlags(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T12:00:01Z","event":"LoadGame","FID":"F1","Commander":"Jameson","Horizons":true,"Odyssey":false,"gameversion":"4.0.0.1903","build":"r12345/r0 "}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Event != EventLoadGame {
		t.Fatal("wrong event")
	}
	var lg LoadGameEvent
	if err := json.Unmarshal(raw.Payload, &lg); err != nil {
		t.Fatal(err)
	}
	if lg.Horizons == nil || *lg.Horizons != true {
		t.Errorf("Horizons = %v", lg.Horizons)
	}
	if lg.Odyssey == nil || *lg.Odyssey != false {
		t.Errorf("Odyssey = %v", lg.Odyssey)
	}
	if lg.GameVersion != "4.0.0.1903" {
		t.Errorf("GameVersion = %q", lg.GameVersion)
	}
}

func TestParseLoadGame_OmittedDLCStaysNil(t *testing.T) {
	// Older clients didn't include Horizons/Odyssey at all. The pointer
	// must round-trip as nil rather than collapsing to false.
	line := []byte(`{"timestamp":"2026-05-21T12:00:01Z","event":"LoadGame","Commander":"X","FID":"F1"}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	var lg LoadGameEvent
	if err := json.Unmarshal(raw.Payload, &lg); err != nil {
		t.Fatal(err)
	}
	if lg.Horizons != nil {
		t.Errorf("missing Horizons must parse as nil; got %v", *lg.Horizons)
	}
	if lg.Odyssey != nil {
		t.Errorf("missing Odyssey must parse as nil; got %v", *lg.Odyssey)
	}
}

func TestParseLocationLike_StarPos(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T12:00:00Z","event":"FSDJump","StarSystem":"Sol","SystemAddress":10477373803,"StarPos":[1.5,-2.5,3.5]}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	var le LocationLikeEvent
	if err := json.Unmarshal(raw.Payload, &le); err != nil {
		t.Fatal(err)
	}
	want := [3]float64{1.5, -2.5, 3.5}
	if le.StarPos != want {
		t.Errorf("StarPos = %v, want %v", le.StarPos, want)
	}
	if le.StarSystem != "Sol" || le.SystemAddress != 10477373803 {
		t.Errorf("system fields wrong: %+v", le)
	}
}

func TestParseCarrierJump_HasStationAndPos(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T12:00:00Z","event":"CarrierJump","Docked":true,"StationName":"MY-FC ABC-12X","StationType":"FleetCarrier","MarketID":3700000123,"StarSystem":"Sol","SystemAddress":100,"StarPos":[0.0,0.0,0.0]}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	var ce CarrierJumpEvent
	if err := json.Unmarshal(raw.Payload, &ce); err != nil {
		t.Fatal(err)
	}
	if ce.MarketID != 3700000123 || ce.StationType != "FleetCarrier" || !ce.Docked {
		t.Errorf("CarrierJump fields wrong: %+v", ce)
	}
}
