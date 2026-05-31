package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadStateSeed_FiltersToStatePreamble(t *testing.T) {
	dir := t.TempDir()
	// A realistic preamble interleaved with activity events. Only the
	// state-snapshot lines should come back, in file order, flagged Replayed.
	lines := []string{
		`{"timestamp":"2026-05-21T12:00:00Z","event":"Fileheader","gameversion":"4.0.0"}`,
		`{"timestamp":"2026-05-21T12:00:01Z","event":"Commander","Name":"Jameson","FID":"F1"}`,
		`{"timestamp":"2026-05-21T12:00:02Z","event":"LoadGame","Commander":"Jameson","Credits":1000}`,
		`{"timestamp":"2026-05-21T12:00:03Z","event":"Materials","Raw":[{"Name":"iron","Count":300}]}`,
		`{"timestamp":"2026-05-21T12:00:04Z","event":"Loadout","Ship":"anaconda","ShipID":7}`,
		`{"timestamp":"2026-05-21T12:00:05Z","event":"Rank","Combat":3}`,
		`{"timestamp":"2026-05-21T12:00:06Z","event":"FSDJump","StarSystem":"Sol"}`,
		`{"timestamp":"2026-05-21T12:00:07Z","event":"Cargo","Vessel":"Ship","Count":4}`,
		`{"timestamp":"2026-05-21T12:00:08Z","event":"Docked","StationName":"Abe"}`,
	}
	var blob string
	for _, l := range lines {
		blob += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "Journal.001.log"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}

	seeds, err := ReadStateSeed(dir)
	if err != nil {
		t.Fatalf("ReadStateSeed: %v", err)
	}
	want := []string{"Fileheader", "Commander", "LoadGame", "Materials", "Loadout", "Cargo"}
	if len(seeds) != len(want) {
		t.Fatalf("got %d seed events, want %d: %v", len(seeds), len(want), eventList(seeds))
	}
	for i, w := range want {
		if seeds[i].Event != w {
			t.Errorf("seed[%d] = %s, want %s (order must follow the file)", i, seeds[i].Event, w)
		}
		if !seeds[i].Replayed {
			t.Errorf("seed[%d] (%s) must be flagged Replayed", i, seeds[i].Event)
		}
	}
}

func TestReadStateSeed_MissingDir(t *testing.T) {
	if _, err := ReadStateSeed(t.TempDir()); err == nil {
		t.Error("expected error when no journal file is present")
	}
}

func eventList(rs []Raw) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Event
	}
	return out
}
