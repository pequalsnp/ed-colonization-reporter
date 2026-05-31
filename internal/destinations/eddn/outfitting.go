package eddn

import (
	"regexp"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// schemaOutfittingV2 is the production schemaRef for outfitting/2 messages.
const schemaOutfittingV2 = "https://eddn.edcd.io/schemas/outfitting/2"

// outfittingModulePattern matches the module symbolic names EDDN's
// outfitting/2 schema accepts. Per the schema: weapons (Hpt_), internals
// (Int_), and armour bulkheads (_Armour_). Anything else — bobbleheads,
// paintjobs, decals, rank/purchase-locked cosmetics — is excluded.
var outfittingModulePattern = regexp.MustCompile(`(?i)(^Hpt_|^Int_|_Armour_)`)

// buildOutfittingMessage transforms Outfitting.json (already loaded) into the
// EDDN outfitting/2 message body. Returns nil if no schema-eligible modules
// remain after filtering.
//
// The schema is strict (additionalProperties:false); required fields are
// systemName, stationName, marketId, timestamp, modules (≥1, unique).
func buildOutfittingMessage(of *journal.OutfittingFile, sess *state.Session) map[string]any {
	if of == nil || of.StarSystem == "" || of.StationName == "" {
		return nil
	}
	seen := map[string]bool{}
	modules := make([]string, 0, len(of.Items))
	for _, it := range of.Items {
		if it.Name == "" || !outfittingModulePattern.MatchString(it.Name) {
			continue
		}
		if seen[it.Name] {
			continue
		}
		seen[it.Name] = true
		modules = append(modules, it.Name)
	}
	if len(modules) == 0 {
		return nil
	}

	msg := map[string]any{
		"systemName":  of.StarSystem,
		"stationName": of.StationName,
		"marketId":    of.MarketID,
		"timestamp":   of.Timestamp,
		"modules":     modules,
	}
	addDLCFlags(msg, sess)
	return msg
}

// addDLCFlags attaches horizons/odyssey to a message when LoadGame told us.
// Per EDDN: only include when known; never serialise an unknown as false.
func addDLCFlags(msg map[string]any, sess *state.Session) {
	if h, _ := sess.DLCFlags(); h != nil {
		msg["horizons"] = *h
	}
	if _, o := sess.DLCFlags(); o != nil {
		msg["odyssey"] = *o
	}
}
