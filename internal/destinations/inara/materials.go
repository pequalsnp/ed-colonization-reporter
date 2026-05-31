package inara

import (
	"encoding/json"
	"sort"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
)

// materialItem is the {Name, Count} shape used by the Materials snapshot,
// EngineerCraft ingredients, Synthesis/TechnologyBroker materials, and
// MissionCompleted material rewards.
type materialItem struct {
	Name  string `json:"Name"`
	Count int    `json:"Count"`
}

// applyMaterialEvent folds a single material-affecting journal event into the
// running inventory map `m` (keyed by lowercase material symbol) and reports
// whether the map changed.
//
// The Materials event is a full snapshot the game writes once per session
// (at LoadGame); the rest are deltas. There is no Materials.json sidecar to
// re-read — unlike Cargo.json — so replaying these increments is the only way
// to keep the engineering-materials tally current. We push the resulting map
// to Inara as setCommanderInventoryMaterials, which is what feeds the
// blueprint "shopping list" on the player's profile.
func applyMaterialEvent(m map[string]int, raw journal.Raw) bool {
	switch raw.Event {
	case journal.EventMaterials:
		var e struct {
			Raw          []materialItem `json:"Raw"`
			Manufactured []materialItem `json:"Manufactured"`
			Encoded      []materialItem `json:"Encoded"`
		}
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		// A snapshot fully replaces the tally.
		for k := range m {
			delete(m, k)
		}
		for _, group := range [][]materialItem{e.Raw, e.Manufactured, e.Encoded} {
			for _, it := range group {
				if it.Name != "" && it.Count > 0 {
					m[it.Name] += it.Count
				}
			}
		}
		return true

	case journal.EventMaterialCollected:
		var e materialItem
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		return addMaterial(m, e.Name, e.Count)

	case journal.EventMaterialDiscarded:
		var e materialItem
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		return addMaterial(m, e.Name, -e.Count)

	case journal.EventScientificResearch:
		// Donating materials at an Engineer / community goal consumes them.
		var e materialItem
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		return addMaterial(m, e.Name, -e.Count)

	case journal.EventMaterialTrade:
		var e struct {
			Paid     materialTradeSide `json:"Paid"`
			Received materialTradeSide `json:"Received"`
		}
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		changed := addMaterial(m, e.Paid.Material, -e.Paid.Quantity)
		changed = addMaterial(m, e.Received.Material, e.Received.Quantity) || changed
		return changed

	case journal.EventEngineerCraft:
		var e struct {
			Ingredients []materialItem `json:"Ingredients"`
		}
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		return subtractAll(m, e.Ingredients)

	case journal.EventSynthesis:
		var e struct {
			Materials []materialItem `json:"Materials"`
		}
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		return subtractAll(m, e.Materials)

	case journal.EventTechnologyBroker:
		var e struct {
			Materials []materialItem `json:"Materials"`
		}
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		return subtractAll(m, e.Materials)

	case journal.EventMissionCompleted:
		var e struct {
			MaterialsReward []materialItem `json:"MaterialsReward"`
		}
		if json.Unmarshal(raw.Payload, &e) != nil {
			return false
		}
		changed := false
		for _, it := range e.MaterialsReward {
			changed = addMaterial(m, it.Name, it.Count) || changed
		}
		return changed
	}
	return false
}

// materialTradeSide is one half of a MaterialTrade (Paid / Received).
type materialTradeSide struct {
	Material string `json:"Material"`
	Quantity int    `json:"Quantity"`
}

// subtractAll consumes a list of ingredients from the tally.
func subtractAll(m map[string]int, items []materialItem) bool {
	changed := false
	for _, it := range items {
		changed = addMaterial(m, it.Name, -it.Count) || changed
	}
	return changed
}

// addMaterial applies a signed delta to a material count, clamping at zero
// and pruning entries that reach zero. Returns whether the map changed.
func addMaterial(m map[string]int, name string, delta int) bool {
	if name == "" || delta == 0 {
		return false
	}
	old := m[name]
	n := old + delta
	if n < 0 {
		n = 0
	}
	if n == old {
		return false
	}
	if n == 0 {
		delete(m, name)
	} else {
		m[name] = n
	}
	return true
}

// buildMaterialsEvent renders the current tally as a setCommanderInventoryMaterials
// event. Rows are sorted by itemName so the payload is deterministic.
func buildMaterialsEvent(m map[string]int, timestamp string) Event {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	rows := make([]map[string]any, 0, len(names))
	for _, n := range names {
		rows = append(rows, map[string]any{"itemName": n, "itemCount": m[n]})
	}
	return Event{Name: EventSetCommanderInventoryMaterials, Timestamp: timestamp, Data: rows}
}
