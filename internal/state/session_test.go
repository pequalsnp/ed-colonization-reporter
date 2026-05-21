package state

import "testing"

func TestSession_BasicFlow(t *testing.T) {
	s := New()
	s.SetCommander("Jameson", "F123")
	s.SetSystem("Sol", 10477373803)

	if got := s.Commander(); got != "Jameson" {
		t.Errorf("commander = %q", got)
	}
	name, addr := s.System()
	if name != "Sol" || addr != 10477373803 {
		t.Errorf("system = (%q, %d)", name, addr)
	}

	s.SetDocked("$EXT_PNL_ColonisationShip:#index=1;", 3789012345, 10477373803)
	docked, station, mkt := s.Dock()
	if !docked || mkt != 3789012345 || station == "" {
		t.Errorf("docked=(%v, %s, %d)", docked, station, mkt)
	}

	s.SetUndocked()
	if d, _, _ := s.Dock(); d {
		t.Error("expected undocked")
	}
}

func TestSession_DockedFillsMissingSystemAddress(t *testing.T) {
	s := New()
	// Game started while already docked — we never saw a Location/FSDJump,
	// but Docked carries a SystemAddress. We should use it.
	s.SetDocked("Foo Station", 1, 999)
	if _, addr := s.System(); addr != 999 {
		t.Errorf("system address = %d, want 999 (filled from Docked)", addr)
	}
}

func TestSession_BuildCache(t *testing.T) {
	s := New()
	if _, ok := s.BuildFor(42); ok {
		t.Error("expected no entry for unknown market")
	}
	s.RememberBuild(42, "build-x")
	id, ok := s.BuildFor(42)
	if !ok || id != "build-x" {
		t.Errorf("BuildFor(42) = (%q, %v)", id, ok)
	}
	// Empty buildID acts as delete.
	s.RememberBuild(42, "")
	if _, ok := s.BuildFor(42); ok {
		t.Error("empty buildID should clear cache entry")
	}
}

func TestSession_Snapshot(t *testing.T) {
	s := New()
	s.SetCommander("Jameson", "F1")
	s.SetSystem("Sol", 100)
	s.SetDocked("Sol Construction", 200, 100)
	snap := s.Snapshot()
	if snap.Commander != "Jameson" || snap.SystemAddress != 100 || snap.MarketID != 200 {
		t.Errorf("snapshot = %+v", snap)
	}
}
