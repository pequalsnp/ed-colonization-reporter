package edcolonize

// Snapshot assembly: fold state.Session (what the outward destinations
// already maintain) together with this package's tracked state (what only
// edcolonize cares about) into one wire payload.

import (
	"encoding/json"
	"sort"
	"time"
)

// buildLocked assembles a snapshot. Caller must hold u.mu.
//
// Returns nil when no commander is known yet — that happens between process
// start and the first LoadGame, and an unattributed snapshot would be
// rejected by the receiver anyway.
func (u *Uploader) buildLocked() *Snapshot {
	commander := u.sess.Commander()
	if commander == "" {
		return nil
	}
	sessSnap := u.sess.Snapshot()

	snap := &Snapshot{
		SchemaVersion: SchemaVersion,
		// Client clock, stamped when the picture was assembled — NOT the
		// journal event time. The receiver measures staleness against its own
		// received_at and reports the gap between the two as clock skew, so
		// using event time here would look like permanent skew during replay.
		CapturedAt:  time.Now().UTC(),
		Commander:   commander,
		FID:         sessSnap.FID,
		GameVersion: sessSnap.GameVersion,
		GameBuild:   sessSnap.GameBuild,
	}
	horizons, odyssey := u.sess.DLCFlags()
	snap.Horizons, snap.Odyssey = horizons, odyssey

	// ── location ────────────────────────────────────────────────────────
	sysName, sysAddr := u.sess.System()
	snap.Location = Location{
		StarSystem:    sysName,
		SystemAddress: sysAddr,
		Docked:        sessSnap.Docked,
		StationName:   sessSnap.StationName,
		MarketID:      sessSnap.MarketID,
	}
	if pos, ok := u.sess.StarPos(); ok {
		p := pos
		snap.Location.StarPos = &p
	}

	// ── ship + loadout summary ──────────────────────────────────────────
	if shipType, shipName, shipIdent := u.sess.Ship(); shipType != "" {
		ship := &Ship{Type: shipType, Name: shipName, Ident: shipIdent}
		if raw, ok := u.sess.ShipLoadout(); ok {
			var lo loadoutEvent
			// A loadout we can't parse is not worth failing the snapshot
			// over — we still know the ship type from the session.
			if err := json.Unmarshal(raw, &lo); err == nil {
				ship.HullValue = lo.HullValue
				ship.Rebuy = lo.Rebuy
				ship.CargoCapacity = lo.CargoCapacity
				ship.MaxJumpRange = lo.MaxJumpRange
				if lo.FuelCapacity != nil {
					main := lo.FuelCapacity.Main
					ship.FuelCapacity = &main
				}
				ship.Modules = summariseModules(lo.Modules)
			}
		}
		snap.Ship = ship
	}

	// ── cargo ───────────────────────────────────────────────────────────
	if cargo, _ := u.sess.ShipCargo(); len(cargo) > 0 {
		names := make([]string, 0, len(cargo))
		for name := range cargo {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			snap.Cargo = append(snap.Cargo, CargoItem{Name: name, Count: cargo[name]})
		}
	}

	// ── carriers ────────────────────────────────────────────────────────
	for _, c := range u.sess.OwnedCarriers() {
		carrier := Carrier{
			MarketID:      c.MarketID,
			Name:          c.Name,
			Callsign:      c.Callsign,
			StarSystem:    c.StarSystem,
			SystemAddress: c.SystemAddress,
		}
		if fcCargo, ok := u.sess.FCCargo(c.MarketID); ok && len(fcCargo) > 0 {
			names := make([]string, 0, len(fcCargo))
			for name := range fcCargo {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				carrier.Cargo = append(carrier.Cargo, CargoItem{Name: name, Count: fcCargo[name]})
			}
		}
		snap.Carriers = append(snap.Carriers, carrier)
	}

	// ── sections only this package tracks ───────────────────────────────
	//
	// These are COPIED, not aliased. post() marshals the finished snapshot on
	// the flush goroutine after u.mu has been released, while HandleEvent may
	// still be folding new events into tracked state — so handing out a live
	// pointer is a data race, not merely an aliasing smell.
	//
	// Ranks is the one that actually bites: the Rank and Progress handlers
	// mutate the pointed-to struct FIELD BY FIELD (r.Combat, r.Trade, …)
	// rather than replacing it, so a rank-up landing mid-marshal races. The
	// others are pointer-swapped wholesale today and would be safe aliased,
	// but they're copied too so this doesn't depend on that staying true.
	if u.tracked.credits != nil {
		credits := *u.tracked.credits
		snap.Credits = &credits
	}
	if u.tracked.ranks != nil {
		ranks := *u.tracked.ranks
		snap.Ranks = &ranks
	}
	if u.tracked.materials != nil {
		// Shallow copy: the slices inside are rebuilt from scratch by
		// toMaterialItems on every Materials event and never mutated in
		// place, so they are safe to share.
		materials := *u.tracked.materials
		snap.Materials = &materials
	}

	if len(u.tracked.missions) > 0 {
		snap.Missions = make([]Mission, 0, len(u.tracked.missions))
		for _, m := range u.tracked.missions {
			snap.Missions = append(snap.Missions, m)
		}
		sort.Slice(snap.Missions, func(i, j int) bool {
			return snap.Missions[i].MissionID < snap.Missions[j].MissionID
		})
	}

	if len(u.tracked.depots) > 0 {
		snap.Colonisation = make([]Depot, 0, len(u.tracked.depots))
		for _, d := range u.tracked.depots {
			snap.Colonisation = append(snap.Colonisation, d)
		}
		sort.Slice(snap.Colonisation, func(i, j int) bool {
			return snap.Colonisation[i].MarketID < snap.Colonisation[j].MarketID
		})
	}

	return snap
}

// summariseModules reduces a full Loadout module list to the fields an
// advice-giving agent reasons about. Drops health, priority, ammo and the
// per-modifier engineering arrays — that's most of the Loadout's bulk and
// none of its usefulness here.
func summariseModules(in []loadoutModule) []Module {
	if len(in) == 0 {
		return nil
	}
	out := make([]Module, 0, len(in))
	for _, m := range in {
		mod := Module{Slot: m.Slot, Item: m.Item, On: m.On}
		if m.Engineering != nil {
			mod.Blueprint = m.Engineering.BlueprintName
			mod.Grade = m.Engineering.Level
			mod.Experiment = firstNonEmpty(
				m.Engineering.ExperimentalEffectLocalised,
				m.Engineering.ExperimentalEffect,
			)
		}
		out = append(out, mod)
	}
	return out
}
