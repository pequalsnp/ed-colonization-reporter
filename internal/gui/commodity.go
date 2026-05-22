package gui

import (
	"sort"
	"strings"
	"unicode"
)

// prettifyCommodity turns a ravencolonial commodity symbol into a
// human-readable name. Inputs come in either underscore-snake form
// ("ceramic_composites", "power_generators") or run-together
// ("cmmcomposite") — we handle both. Known acronyms get uppercased.
//
// This is intentionally a small static table; the universe of journal
// commodity symbols is finite and Frontier doesn't add new ones often.
var commodityNameOverrides = map[string]string{
	"cmmcomposite":      "CMM Composite",
	"hydrogen_fuel":     "Hydrogen Fuel",
	"surfacestabilisers": "Surface Stabilisers",
	"medicaldiagnosticequipment": "Medical Diagnostic Equipment",
	"powergenerators":   "Power Generators",
	"foodcartridges":    "Food Cartridges",
	"fruitandvegetables": "Fruit and Vegetables",
	"liquidoxygen":      "Liquid Oxygen",
	"survivalequipment": "Survival Equipment",
	"evacuationshelter": "Evacuation Shelter",
	"buildingfabricators": "Building Fabricators",
	"emergencypowercells": "Emergency Power Cells",
	"computercomponents": "Computer Components",
	"landenrichmentsystems": "Land Enrichment Systems",
	"medicaldiagnosticeqpt": "Medical Diagnostic Equipment",
	"structuralregulators": "Structural Regulators",
	"nonlethalweapons":  "Non-Lethal Weapons",
	"ceramiccomposites": "Ceramic Composites",
	"semiconductors":    "Semiconductors",
	"superconductors":   "Superconductors",
}

// upperAcronyms are words that should be uppercased on prettification.
var upperAcronyms = map[string]bool{
	"cmm": true, "id": true, "ai": true, "hud": true,
}

// PrettifyCommodity returns a display-friendly name for a ravencolonial
// commodity symbol.
func PrettifyCommodity(sym string) string {
	if sym == "" {
		return ""
	}
	if v, ok := commodityNameOverrides[strings.ToLower(sym)]; ok {
		return v
	}
	// Split on underscores; if none, the input is run-together: insert
	// spaces before lowercased capital letters (not applicable since the
	// symbols are usually all-lowercase). For run-together inputs we
	// can't recover word boundaries without a dictionary, so just title-case.
	parts := strings.Split(sym, "_")
	for i, p := range parts {
		if upperAcronyms[strings.ToLower(p)] {
			parts[i] = strings.ToUpper(p)
		} else {
			parts[i] = titleCase(p)
		}
	}
	return strings.Join(parts, " ")
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	for i, r := range runes {
		if i == 0 {
			runes[i] = unicode.ToUpper(r)
		} else {
			runes[i] = unicode.ToLower(r)
		}
	}
	return string(runes)
}

// topCommodities returns the top N commodities by quantity from the
// outstanding map, sorted descending. Ties broken alphabetically.
func topCommodities(m map[string]int, n int) []commodityEntry {
	entries := make([]commodityEntry, 0, len(m))
	for k, v := range m {
		if v <= 0 {
			continue
		}
		entries = append(entries, commodityEntry{Symbol: k, Count: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Symbol < entries[j].Symbol
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	return entries
}

type commodityEntry struct {
	Symbol string
	Count  int
}
