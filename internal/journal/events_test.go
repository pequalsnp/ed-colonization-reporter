package journal

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseLine_Commander(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T12:30:45Z","event":"Commander","FID":"F1234","Name":"Jameson"}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if raw.Event != EventCommander {
		t.Errorf("event = %q, want %q", raw.Event, EventCommander)
	}
	var cmdr CommanderEvent
	if err := json.Unmarshal(raw.Payload, &cmdr); err != nil {
		t.Fatalf("decode commander: %v", err)
	}
	if cmdr.Name != "Jameson" {
		t.Errorf("name = %q, want Jameson", cmdr.Name)
	}
	if cmdr.FID != "F1234" {
		t.Errorf("FID = %q, want F1234", cmdr.FID)
	}
}

func TestParseLine_ConstructionDepot(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T13:00:00Z","event":"ColonisationConstructionDepot","MarketID":3789012345,"ConstructionProgress":0.42,"ConstructionComplete":false,"ConstructionFailed":false,"ResourcesRequired":[{"Name":"$titanium_name;","Name_Localised":"Titanium","RequiredAmount":1000,"ProvidedAmount":420,"Payment":12345}]}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if raw.Event != EventColonisationConstructionDepot {
		t.Fatalf("event = %q", raw.Event)
	}
	var depot ColonisationConstructionDepotEvent
	if err := json.Unmarshal(raw.Payload, &depot); err != nil {
		t.Fatalf("decode depot: %v", err)
	}
	if depot.MarketID != 3789012345 {
		t.Errorf("MarketID = %d", depot.MarketID)
	}
	if len(depot.ResourcesRequired) != 1 {
		t.Fatalf("ResourcesRequired len = %d", len(depot.ResourcesRequired))
	}
	r := depot.ResourcesRequired[0]
	if r.NameLocalised != "Titanium" || r.RequiredAmount != 1000 || r.ProvidedAmount != 420 {
		t.Errorf("unexpected resource row: %+v", r)
	}
}

func TestParseLine_Contribution(t *testing.T) {
	line := []byte(`{"timestamp":"2026-05-21T13:05:00Z","event":"ColonisationContribution","MarketID":3789012345,"Contributions":[{"Name":"$titanium_name;","Name_Localised":"Titanium","Amount":160}]}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if raw.Event != EventColonisationContribution {
		t.Fatalf("event = %q", raw.Event)
	}
	var contrib ColonisationContributionEvent
	if err := json.Unmarshal(raw.Payload, &contrib); err != nil {
		t.Fatalf("decode contribution: %v", err)
	}
	if contrib.Contributions[0].Amount != 160 {
		t.Errorf("amount = %d", contrib.Contributions[0].Amount)
	}
}

func TestParseLine_LeadingBOM(t *testing.T) {
	line := []byte("\xef\xbb\xbf" + `{"timestamp":"2026-05-21T12:30:45Z","event":"Commander","FID":"F1","Name":"X"}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine with BOM: %v", err)
	}
	if raw.Event != EventCommander {
		t.Errorf("event = %q", raw.Event)
	}
}

func TestParseLine_Empty(t *testing.T) {
	for _, in := range [][]byte{nil, {}, []byte("   "), []byte("\n\r\t  ")} {
		if _, err := ParseLine(in); !errors.Is(err, ErrEmptyLine) {
			t.Errorf("ParseLine(%q) err = %v, want ErrEmptyLine", in, err)
		}
	}
}

func TestParseLine_MissingEvent(t *testing.T) {
	if _, err := ParseLine([]byte(`{"timestamp":"2026-05-21T12:30:45Z"}`)); err == nil {
		t.Error("expected error when event field is missing")
	}
}

func TestParseLine_Garbage(t *testing.T) {
	if _, err := ParseLine([]byte(`not json`)); err == nil {
		t.Error("expected error for non-JSON input")
	}
}

func TestParseLine_PayloadIsIndependentCopy(t *testing.T) {
	// Mutating the input buffer after ParseLine returns must not corrupt
	// the captured payload — this matters for bufio.Scanner callers, whose
	// buffer is reused on each Scan().
	line := []byte(`{"timestamp":"2026-05-21T12:30:45Z","event":"Commander","FID":"F1","Name":"X"}`)
	raw, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	for i := range line {
		line[i] = 0
	}
	var cmdr CommanderEvent
	if err := json.Unmarshal(raw.Payload, &cmdr); err != nil {
		t.Fatalf("decode after input mutation: %v", err)
	}
	if cmdr.Name != "X" {
		t.Errorf("name = %q, want X — payload was not isolated from input mutation", cmdr.Name)
	}
}
