package eddn

import (
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func TestUploader_AccessorsRoundtrip(t *testing.T) {
	u := New(SoftwareID{Name: "t", Version: "v"}, state.New())
	if u.Name() != "EDDN" {
		t.Errorf("Name = %q, want EDDN", u.Name())
	}
	if u.Enabled() {
		t.Error("default Enabled should be false")
	}
	u.SetEnabled(true)
	if !u.Enabled() {
		t.Error("after SetEnabled(true), Enabled() should report true")
	}
	u.SetEnabled(false)
	if u.Enabled() {
		t.Error("SetEnabled(false) should turn it off")
	}
}

func TestShortSchema(t *testing.T) {
	cases := map[string]string{
		"https://eddn.edcd.io/schemas/journal/1":       "journal/1",
		"https://eddn.edcd.io/schemas/commodity/3/test": "commodity/3/test",
		"https://elsewhere.example/foo":                "https://elsewhere.example/foo",
		"":                                             "",
	}
	for in, want := range cases {
		if got := shortSchema(in); got != want {
			t.Errorf("shortSchema(%q) = %q, want %q", in, got, want)
		}
	}
}
