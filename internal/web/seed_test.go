package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
)

// TestSeedStateFromJournal_SeedsSessionState verifies that the launch-time
// seed pass (used when session replay is off) populates commander identity
// and ship loadout from the journal preamble, flowing through the multiplex
// to the reporter as replayed events.
func TestSeedStateFromJournal_SeedsSessionState(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	s.rep.JournalDir = dir
	// A minimal mux with just the reporter is enough to observe session
	// seeding; the Inara materials path is covered in the inara package.
	s.mux = destinations.NewMultiplex(s.rep)

	lines := []string{
		`{"timestamp":"2026-05-21T12:00:00Z","event":"Fileheader","gameversion":"4.0.0"}`,
		`{"timestamp":"2026-05-21T12:00:01Z","event":"Commander","Name":"Jameson","FID":"F1"}`,
		`{"timestamp":"2026-05-21T12:00:02Z","event":"LoadGame","Commander":"Jameson"}`,
		`{"timestamp":"2026-05-21T12:00:03Z","event":"FSDJump","StarSystem":"Sol"}`, // not state — must be ignored
		`{"timestamp":"2026-05-21T12:00:04Z","event":"Loadout","Ship":"anaconda","ShipID":7,"ShipName":"Voyager","ShipIdent":"VY-1","Modules":[]}`,
	}
	var blob string
	for _, l := range lines {
		blob += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "Journal.001.log"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}

	s.seedStateFromJournal(context.Background(), dir)

	if got := s.session.Commander(); got != "Jameson" {
		t.Errorf("commander not seeded: got %q", got)
	}
	label, ok := s.CurrentShip()
	if !ok || label != "Voyager" {
		t.Errorf("ship not seeded: got %q, ok=%v", label, ok)
	}
	if _, ok := s.EDSYShipURL(); !ok {
		t.Error("EDSY ship URL should be available after seeding the Loadout")
	}
}
