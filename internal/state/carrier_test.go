package state

import "testing"

func TestRegisterOwnedCarrier_BasicAndMerge(t *testing.T) {
	s := New()
	if s.IsOwnedCarrier(42) {
		t.Error("empty session should not own market 42")
	}

	s.RegisterOwnedCarrier(OwnedCarrier{MarketID: 42, Name: "DREAMSTRIDER", Callsign: "ABC-12X"})
	if !s.IsOwnedCarrier(42) {
		t.Error("expected owned after registration")
	}
	c, ok := s.OwnedCarrier(42)
	if !ok || c.Name != "DREAMSTRIDER" || c.Callsign != "ABC-12X" {
		t.Errorf("got %+v", c)
	}

	// Second registration adds system info but does NOT clobber name/callsign.
	s.RegisterOwnedCarrier(OwnedCarrier{MarketID: 42, StarSystem: "Sol", SystemAddress: 100})
	c, _ = s.OwnedCarrier(42)
	if c.Name != "DREAMSTRIDER" || c.Callsign != "ABC-12X" {
		t.Errorf("merge clobbered: %+v", c)
	}
	if c.StarSystem != "Sol" || c.SystemAddress != 100 {
		t.Errorf("system not merged in: %+v", c)
	}
}

func TestRegisterOwnedCarrier_ZeroMarketIsNoop(t *testing.T) {
	s := New()
	s.RegisterOwnedCarrier(OwnedCarrier{MarketID: 0, Name: "X"})
	if s.IsOwnedCarrier(0) {
		t.Error("zero MarketID should not be registered")
	}
}

func TestIsOwnedCarrier_UnknownMarket(t *testing.T) {
	s := New()
	s.RegisterOwnedCarrier(OwnedCarrier{MarketID: 1})
	if s.IsOwnedCarrier(2) {
		t.Error("market 2 should not be owned")
	}
}
