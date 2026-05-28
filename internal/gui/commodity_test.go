package gui

import "testing"

func TestPrettifyCommodity(t *testing.T) {
	cases := map[string]string{
		"cmmcomposite":              "CMM Composite",
		"titanium":                  "Titanium",
		"ceramic_composites":        "Ceramic Composites",
		"liquidoxygen":              "Liquid Oxygen",
		"surface_stabilisers":       "Surface Stabilisers",
		"non_lethal_weapons":        "Non Lethal Weapons", // underscore-form, not in override table — title-cased
		"insulatingmembrane":        "Insulating Membrane",
		"geologicalequipment":       "Geological Equipment",
		"bioreducinglichen":         "Bio-Reducing Lichen",
		"resonatingseparators":      "Resonating Separators",
		"hazardousenvironmentsuits": "H.E. Suits",
		"hesuits":                   "H.E. Suits",
		"mutomimager":               "Muon Imager",
		"muonimager":                "Muon Imager",
		"":                          "",
	}
	for in, want := range cases {
		if got := PrettifyCommodity(in); got != want {
			t.Errorf("PrettifyCommodity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTopCommodities(t *testing.T) {
	m := map[string]int{
		"titanium": 5760,
		"steel":    10164,
		"copper":   407,
		"gold":     0, // zero should be excluded
		"silver":   100,
	}
	got := topCommodities(m, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	want := []string{"steel", "titanium", "copper"}
	for i, c := range got {
		if c.Symbol != want[i] {
			t.Errorf("[%d] = %s, want %s", i, c.Symbol, want[i])
		}
	}
}

func TestTopCommodities_TieBreakAlphabetical(t *testing.T) {
	m := map[string]int{"alpha": 100, "beta": 100, "gamma": 100}
	got := topCommodities(m, 3)
	if got[0].Symbol != "alpha" || got[1].Symbol != "beta" || got[2].Symbol != "gamma" {
		t.Errorf("tie-break ordering wrong: %v", got)
	}
}
