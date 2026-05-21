package inara

import (
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func TestUploader_AccessorsRoundtrip(t *testing.T) {
	u := New(SoftwareID{Name: "t", Version: "v"}, state.New())
	if u.Name() != "Inara" {
		t.Errorf("Name = %q, want Inara", u.Name())
	}
	if u.Enabled() {
		t.Error("default Enabled should be false")
	}
	u.SetEnabled(true)
	if !u.Enabled() {
		t.Error("Enabled after SetEnabled(true)")
	}
	u.SetAPIKey("xyz")
	if u.apiKey != "xyz" {
		t.Errorf("apiKey = %q", u.apiKey)
	}
}

func TestExtractFloat(t *testing.T) {
	cases := []struct {
		payload string
		key     string
		want    float64
	}{
		{`{"JumpDist":14.3}`, "JumpDist", 14.3},
		{`{"JumpDist":14}`, "JumpDist", 14},
		{`{"Other":5}`, "JumpDist", 0},
		{`{"JumpDist":"bogus"}`, "JumpDist", 0},
		{`not json`, "JumpDist", 0},
	}
	for _, tc := range cases {
		got := extractFloat([]byte(tc.payload), tc.key)
		if got != tc.want {
			t.Errorf("extractFloat(%q, %q) = %v, want %v", tc.payload, tc.key, got, tc.want)
		}
	}
}
