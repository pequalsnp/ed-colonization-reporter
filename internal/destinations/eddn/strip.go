package eddn

import "strings"

// stripLocalised walks a parsed JSON tree and deletes every key ending in
// "_Localised". The EDDN journal schema rejects any payload containing
// these via patternProperties.
func stripLocalised(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if strings.HasSuffix(k, "_Localised") {
				delete(t, k)
				continue
			}
			stripLocalised(child)
		}
	case []any:
		for _, child := range t {
			stripLocalised(child)
		}
	}
}

// Top-level keys EDDN forbids on journal/1 messages, regardless of event.
// Sourced from schemas/journal-v1.0.json in github.com/EDCD/EDDN (live branch).
var globalForbidden = []string{
	"ActiveFine", "CockpitBreach", "BoostUsed", "FuelLevel", "FuelUsed",
	"JumpDist", "Latitude", "Longitude", "Wanted", "IsNewEntry",
	"NewTraitsDiscovered", "Traits", "VoucherAmount",
}

// Faction-level keys EDDN forbids (must be removed from each item in a
// Factions array). The Faction array itself stays.
var factionForbidden = []string{
	"HappiestSystem", "HomeSystem", "MyReputation", "SquadronFaction",
}

// stripJournalForbidden removes the EDDN-forbidden top-level and per-faction
// keys from a parsed journal event. The map is mutated in place.
func stripJournalForbidden(m map[string]any) {
	for _, k := range globalForbidden {
		delete(m, k)
	}
	if factions, ok := m["Factions"].([]any); ok {
		for _, f := range factions {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			for _, k := range factionForbidden {
				delete(fm, k)
			}
		}
	}
}
