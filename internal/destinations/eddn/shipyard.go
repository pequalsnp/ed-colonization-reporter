package eddn

import (
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// schemaShipyardV2 is the production schemaRef for shipyard/2 messages.
const schemaShipyardV2 = "https://eddn.edcd.io/schemas/shipyard/2"

// buildShipyardMessage transforms Shipyard.json (already loaded) into the
// EDDN shipyard/2 message body. Returns nil if the shipyard sells nothing.
//
// The schema is strict (additionalProperties:false); required fields are
// systemName, stationName, marketId, timestamp, ships (≥1, unique).
func buildShipyardMessage(sf *journal.ShipyardFile, sess *state.Session) map[string]any {
	if sf == nil || sf.StarSystem == "" || sf.StationName == "" {
		return nil
	}
	seen := map[string]bool{}
	ships := make([]string, 0, len(sf.PriceList))
	for _, s := range sf.PriceList {
		if s.ShipType == "" || seen[s.ShipType] {
			continue
		}
		seen[s.ShipType] = true
		ships = append(ships, s.ShipType)
	}
	if len(ships) == 0 {
		return nil
	}

	msg := map[string]any{
		"systemName":  sf.StarSystem,
		"stationName": sf.StationName,
		"marketId":    sf.MarketID,
		"timestamp":   sf.Timestamp,
		"ships":       ships,
	}
	if sf.AllowCobraMkIV != nil {
		msg["allowCobraMkIV"] = *sf.AllowCobraMkIV
	}
	addDLCFlags(msg, sess)
	return msg
}
