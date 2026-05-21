package eddn

import (
	"strings"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// schemaCommodityV3 is the production schemaRef for commodity/3 messages.
const schemaCommodityV3 = "https://eddn.edcd.io/schemas/commodity/3"

// nonMarketableCategories are categories EDDN rejects on commodity/3.
// Limpets are the main culprit; they're a "commodity" in journal but not
// a market-traded good.
var nonMarketableCategories = map[string]bool{
	"$market_category_nonmarketable;": true,
	"nonmarketable":                   true,
}

// buildCommodityMessage transforms Market.json (already loaded) into the
// EDDN commodity/3 message body. Returns nil if the market has no
// commodities worth uploading after filtering.
//
// EDDN's commodity/3 schema is strict: `additionalProperties: false` on the
// message, so any key not in the rename list is rejected. We must rename
// (e.g. StarSystem -> systemName), strip wrappers (`$titanium_name;` ->
// `titanium`), and skip non-marketable items.
func buildCommodityMessage(mf *journal.MarketFile, sess *state.Session) map[string]any {
	if mf == nil {
		return nil
	}
	commodities := make([]map[string]any, 0, len(mf.Items))
	for _, it := range mf.Items {
		if it.Name == "" {
			continue
		}
		if nonMarketableCategories[strings.ToLower(it.Category)] {
			continue
		}
		commodities = append(commodities, map[string]any{
			"name":           stripCommodityWrapper(it.Name),
			"meanPrice":      it.MeanPrice,
			"buyPrice":       it.BuyPrice,
			"stock":          it.Stock,
			"stockBracket":   it.StockBracket,
			"sellPrice":      it.SellPrice,
			"demand":         it.Demand,
			"demandBracket":  it.DemandBracket,
		})
	}
	if len(commodities) == 0 {
		return nil
	}

	msg := map[string]any{
		"systemName":  mf.StarSystem,
		"stationName": mf.StationName,
		"marketId":    mf.MarketID,
		"timestamp":   mf.Timestamp,
		"commodities": commodities,
	}
	if mf.StationType != "" {
		msg["stationType"] = mf.StationType
	}
	if h, _ := sess.DLCFlags(); h != nil {
		msg["horizons"] = *h
	}
	if _, o := sess.DLCFlags(); o != nil {
		msg["odyssey"] = *o
	}
	return msg
}

// stripCommodityWrapper turns "$titanium_name;" into "titanium". If the
// input isn't wrapped, it's returned lower-cased — the schema expects
// lowercase symbol names.
func stripCommodityWrapper(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimSuffix(s, ";")
	s = strings.TrimSuffix(s, "_name")
	return strings.ToLower(s)
}
