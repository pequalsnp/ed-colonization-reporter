package state

import "testing"

func TestSetDLCFlags_NilPreserved(t *testing.T) {
	s := New()
	// Initially both flags are unknown.
	h, o := s.DLCFlags()
	if h != nil || o != nil {
		t.Errorf("initial DLC flags should be nil; got h=%v o=%v", h, o)
	}

	// SetDLCFlags with nil for both should be a no-op.
	s.SetDLCFlags(nil, nil)
	h, o = s.DLCFlags()
	if h != nil || o != nil {
		t.Errorf("SetDLCFlags(nil,nil) clobbered state; got h=%v o=%v", h, o)
	}

	yes := true
	s.SetDLCFlags(&yes, nil)
	h, o = s.DLCFlags()
	if h == nil || *h != true {
		t.Errorf("horizons should be true; got h=%v", h)
	}
	if o != nil {
		t.Errorf("odyssey should still be nil after horizons-only set; got %v", o)
	}

	no := false
	s.SetDLCFlags(nil, &no)
	h, o = s.DLCFlags()
	if h == nil || *h != true {
		t.Errorf("horizons must survive odyssey-only set; got %v", h)
	}
	if o == nil || *o != false {
		t.Errorf("odyssey should be false; got %v", o)
	}
}

func TestSetGameVersion_PartialUpdates(t *testing.T) {
	s := New()
	s.SetGameVersion("4.1", "build-1")
	v, b := s.GameVersion()
	if v != "4.1" || b != "build-1" {
		t.Errorf("initial set wrong: v=%q b=%q", v, b)
	}

	// Empty string is treated as "no update" — preserve the previous value.
	s.SetGameVersion("", "build-2")
	v, b = s.GameVersion()
	if v != "4.1" {
		t.Errorf("empty version should not clobber; got v=%q", v)
	}
	if b != "build-2" {
		t.Errorf("build should have updated; got b=%q", b)
	}

	s.SetGameVersion("4.2", "")
	v, b = s.GameVersion()
	if v != "4.2" || b != "build-2" {
		t.Errorf("got v=%q b=%q", v, b)
	}
}

func TestSetSystemWithPos_PopulatesStarPos(t *testing.T) {
	s := New()
	if pos, ok := s.StarPos(); ok {
		t.Errorf("fresh session should have no StarPos; got %v", pos)
	}
	s.SetSystemWithPos("Sol", 100, [3]float64{1, 2, 3})
	pos, ok := s.StarPos()
	if !ok {
		t.Fatal("StarPos should be known after SetSystemWithPos")
	}
	if pos != [3]float64{1, 2, 3} {
		t.Errorf("pos = %v", pos)
	}

	// Subsequent SetSystem (no-pos overload) leaves the cached pos alone —
	// that's deliberate so a Location event without coords doesn't blank
	// the value we already had.
	s.SetSystem("Sol", 100)
	pos, ok = s.StarPos()
	if !ok || pos != [3]float64{1, 2, 3} {
		t.Errorf("SetSystem clobbered StarPos: pos=%v ok=%v", pos, ok)
	}
}
