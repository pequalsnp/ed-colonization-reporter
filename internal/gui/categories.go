package gui

import "strings"

// commodityCategory maps a ravencolonial commodity symbol to its in-game
// commodity-market section name. The categories match Elite Dangerous's
// own market UI so the user can scan our list the same way they scan
// the FC commodity market.
//
// Categories defined by Frontier (alphabetical, per the in-game market):
//   Chemicals, Consumer Items, Foods, Industrial Materials, Legal Drugs,
//   Machinery, Medicines, Metals, Minerals, Salvage, Slaves, Technology,
//   Textiles, Waste, Weapons
//
// "Limpets" isn't a Frontier category but lives in its own pseudo-section
// on the market UI; we honour that. "Other" catches commodities we
// haven't classified yet — the table is finite and stable, so unknowns
// should be rare and easy to add later.
var commodityCategory = map[string]string{
	// Chemicals
	"tritium":            "Chemicals",
	"liquidoxygen":       "Chemicals",
	"surfacestabilisers": "Chemicals",
	"water":              "Chemicals",
	"explosives":         "Chemicals",
	"hydrogenfuel":       "Chemicals",
	"hydrogenperoxide":   "Chemicals",
	"mineraloil":         "Chemicals",
	"nervemedia":         "Chemicals",
	"pesticides":         "Chemicals",

	// Consumer Items
	"clothing":          "Consumer Items",
	"consumertechnology": "Consumer Items",
	"domesticappliances": "Consumer Items",
	"evacuationshelter": "Consumer Items",
	"survivalequipment": "Consumer Items",

	// Foods
	"algae":              "Foods",
	"animalmeat":         "Foods",
	"coffee":             "Foods",
	"fish":               "Foods",
	"foodcartridges":     "Foods",
	"fruitandvegetables": "Foods",
	"grain":              "Foods",
	"syntheticmeat":      "Foods",
	"tea":                "Foods",

	// Industrial Materials
	"ceramiccomposites":  "Industrial Materials",
	"cmmcomposite":       "Industrial Materials",
	"insulatingmembrane": "Industrial Materials",
	"metaalloys":         "Industrial Materials",
	"polymers":           "Industrial Materials",
	"semiconductors":     "Industrial Materials",
	"superconductors":    "Industrial Materials",

	// Legal Drugs
	"beer":         "Legal Drugs",
	"liquor":       "Legal Drugs",
	"narcotics":    "Legal Drugs",
	"onionheadgammastrain": "Legal Drugs",
	"tobacco":      "Legal Drugs",
	"wine":         "Legal Drugs",

	// Machinery
	"atmosphericprocessors": "Machinery",
	"buildingfabricators":   "Machinery",
	"cropharvesters":        "Machinery",
	"emergencypowercells":   "Machinery",
	"geologicalequipment":   "Machinery",
	"heliostaticfurnaces":   "Machinery",
	"hnshockmount":          "Machinery",
	"marineequipment":       "Machinery",
	"microbialfurnaces":     "Machinery",
	"mineralextractors":     "Machinery",
	"modularterminals":      "Machinery",
	"powerconverter":        "Machinery",
	"powergenerators":       "Machinery",
	"powertransferbus":      "Machinery",
	"radiationbaffle":       "Machinery",
	"skimmercomponents":     "Machinery",
	"thermalcoolingunits":   "Machinery",
	"waterpurifiers":        "Machinery",
	"wreckagecomponents":    "Machinery",

	// Medicines
	"advancedmedicines":  "Medicines",
	"agriculturalmedicines": "Medicines",
	"basicmedicines":     "Medicines",
	"combatstabilisers":  "Medicines",
	"performanceenhancers": "Medicines",
	"progenitorcells":    "Medicines",

	// Metals
	"aluminium": "Metals",
	"beryllium": "Metals",
	"bismuth":   "Metals",
	"cobalt":    "Metals",
	"copper":    "Metals",
	"gallium":   "Metals",
	"gold":      "Metals",
	"hafnium178": "Metals",
	"indium":    "Metals",
	"lanthanum": "Metals",
	"lithium":   "Metals",
	"osmium":    "Metals",
	"palladium": "Metals",
	"platinum":  "Metals",
	"praseodymium": "Metals",
	"samarium":  "Metals",
	"silver":    "Metals",
	"steel":     "Metals",
	"tantalum":  "Metals",
	"thallium":  "Metals",
	"thorium":   "Metals",
	"titanium":  "Metals",
	"uranium":   "Metals",

	// Minerals
	"alexandrite":  "Minerals",
	"bauxite":      "Minerals",
	"benitoite":    "Minerals",
	"bertrandite":  "Minerals",
	"bromellite":   "Minerals",
	"coltan":       "Minerals",
	"cryolite":     "Minerals",
	"gallite":      "Minerals",
	"goslarite":    "Minerals",
	"grandidierite": "Minerals",
	"haematite":    "Minerals",
	"indite":       "Minerals",
	"jadeite":      "Minerals",
	"lepidolite":   "Minerals",
	"lithiumhydroxide": "Minerals",
	"lowtemperaturediamond": "Minerals",
	"methaneclathrate":  "Minerals",
	"methanolmonohydratecrystals": "Minerals",
	"monazite":     "Minerals",
	"moissanite":   "Minerals",
	"musgravite":   "Minerals",
	"opal":         "Minerals",
	"painite":      "Minerals",
	"pyrophyllite": "Minerals",
	"rhodplumsite": "Minerals",
	"rutile":       "Minerals",
	"serendibite":  "Minerals",
	"taaffeite":    "Minerals",
	"uraninite":    "Minerals",

	// Salvage
	"aiarelics":             "Salvage",
	"ancientartefact":       "Salvage",
	"ancientkey":            "Salvage",
	"antiqueities":          "Salvage",
	"earthrelics":           "Salvage",
	"galacticindustriallabel": "Salvage",
	"prohibitedresearchmaterials": "Salvage",
	"thargoidheart":         "Salvage",
	"thargoidsensor":        "Salvage",
	"thargoidtitandrivecomponent": "Salvage",
	"unknownartifact":       "Salvage",
	"unstabledatacore":      "Salvage",
	"usscargoancientartefact": "Salvage",
	"usscargotrinketsofhiddenfortune": "Salvage",

	// Technology
	"advancedcatalysers":      "Technology",
	"animalmonitors":          "Technology",
	"aquaponicsystems":        "Technology",
	"autofabricators":         "Technology",
	"bioreducinglichen":       "Technology",
	"computercomponents":      "Technology",
	"hesuits":                    "Technology",
	"hazardousenvironmentsuits":  "Technology", // RC's long-form symbol for H.E. Suits
	"hardwarediagnosticsensor":   "Technology",
	"iondistributor":             "Technology",
	"landenrichmentsystems":      "Technology",
	"medicaldiagnosticequipment": "Technology",
	"microcontrollers":           "Technology",
	"muonimager":                 "Technology",
	"mutomimager":                "Technology", // Frontier's internal symbol for Muon Imager
	"nanobreakers":            "Technology",
	"resonatingseparators":    "Technology",
	"robotics":                "Technology",
	"structuralregulators":    "Technology",
	"telemetrysuite":          "Technology",

	// Textiles
	"conductivefabrics": "Textiles",
	"leather":           "Textiles",
	"militarygradefabrics": "Textiles",
	"naturalfabrics":    "Textiles",
	"syntheticfabrics":  "Textiles",

	// Waste
	"biowaste":          "Waste",
	"chemicalwaste":     "Waste",
	"scrap":             "Waste",
	"toxicwaste":        "Waste",

	// Weapons
	"battleweapons":    "Weapons",
	"landmines":        "Weapons",
	"nonlethalweapons": "Weapons",
	"personalweapons":  "Weapons",
	"reactivearmour":   "Weapons",
	"smallarms":        "Weapons",

	// Limpets (its own pseudo-section in the in-game market)
	"drones":  "Limpets",
	"limpet":  "Limpets",
	"limpets": "Limpets",
}

// categoryOrder is the display order for category sections. Matches the
// in-game commodity market layout. Unknown categories sort to the end
// under "Other".
var categoryOrder = []string{
	"Chemicals",
	"Consumer Items",
	"Foods",
	"Industrial Materials",
	"Legal Drugs",
	"Machinery",
	"Medicines",
	"Metals",
	"Minerals",
	"Salvage",
	"Technology",
	"Textiles",
	"Waste",
	"Weapons",
	"Limpets",
	"Other",
}

// CategoryFor returns the commodity market category for a ravencolonial
// commodity symbol. Falls back to "Other" for symbols we don't have in
// our table — easier to add as we encounter them than to keep an
// exhaustive list out of date.
func CategoryFor(symbol string) string {
	if symbol == "" {
		return "Other"
	}
	if cat, ok := commodityCategory[strings.ToLower(symbol)]; ok {
		return cat
	}
	return "Other"
}
