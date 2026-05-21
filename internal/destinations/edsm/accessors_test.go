package edsm

import (
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func TestUploader_AccessorsRoundtrip(t *testing.T) {
	u := New(SoftwareID{Name: "t", Version: "v"}, state.New())
	if u.Name() != "EDSM" {
		t.Errorf("Name = %q, want EDSM", u.Name())
	}
	if u.Enabled() {
		t.Error("default Enabled should be false")
	}
	u.SetEnabled(true)
	if !u.Enabled() {
		t.Error("Enabled after SetEnabled(true)")
	}
	u.SetAPIKey("xyz")
	if got := u.apiKeyCopy(); got != "xyz" {
		t.Errorf("apiKey = %q", got)
	}
}
