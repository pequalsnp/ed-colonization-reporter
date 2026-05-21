package reporter

import (
	"context"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// TestHandleFileheader_RecordsGameVersion makes sure Fileheader populates
// state.GameVersion/GameBuild, which the EDDN/EDSM/Inara destinations rely
// on for their header fields.
func TestHandleFileheader_RecordsGameVersion(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventFileheader, map[string]any{
		"gameversion": "4.0.0.1903",
		"build":       "r12345/r0 ",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	v, b := sess.GameVersion()
	if v != "4.0.0.1903" || b != "r12345/r0 " {
		t.Errorf("version/build = (%q, %q)", v, b)
	}
}

func TestHandleLoadGame_PopulatesDLCAndGameVersion(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventLoadGame, map[string]any{
		"Commander":   "Jameson",
		"FID":         "F1",
		"Horizons":    true,
		"Odyssey":     false,
		"gameversion": "4.0.0.1903",
		"build":       "r12345/r0 ",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if cmdr := sess.Commander(); cmdr != "Jameson" {
		t.Errorf("commander = %q", cmdr)
	}
	h, o := sess.DLCFlags()
	if h == nil || *h != true {
		t.Errorf("horizons should be true; got %v", h)
	}
	if o == nil || *o != false {
		t.Errorf("odyssey should be false; got %v", o)
	}
	v, b := sess.GameVersion()
	if v != "4.0.0.1903" || b != "r12345/r0 " {
		t.Errorf("game version/build = (%q, %q)", v, b)
	}
}

func TestHandleLoadGame_OmittedDLCStaysUnknown(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	// Pre-3.x LoadGame doesn't carry Horizons/Odyssey at all. We must
	// preserve "unknown" rather than store false.
	raw := mustRaw(t, journal.EventLoadGame, map[string]any{
		"Commander": "Jameson",
		"FID":       "F1",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	h, o := sess.DLCFlags()
	if h != nil || o != nil {
		t.Errorf("omitted DLC fields must round-trip as nil; got h=%v o=%v", h, o)
	}
}

func TestHandleFSDJump_StoresStarPos(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Alpha Centauri",
		"SystemAddress": 12345,
		"StarPos":       []any{3.03, -0.09, 3.03},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	pos, ok := sess.StarPos()
	if !ok {
		t.Fatal("StarPos should be known after FSDJump")
	}
	if pos[0] < 3.0 || pos[0] > 3.1 || pos[2] < 3.0 || pos[2] > 3.1 {
		t.Errorf("StarPos = %v", pos)
	}
}
