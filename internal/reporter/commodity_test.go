package reporter

import "testing"

func TestNormalizeCommodity(t *testing.T) {
	cases := map[string]string{
		"$titanium_name;":              "titanium",
		"$ceramic_composites_name;":    "ceramic_composites",
		"titanium":                     "titanium",
		"Titanium":                     "titanium",
		"  $steel_name;  ":             "steel",
		"":                             "",
		"$Power Generators_name;":      "power generators", // unusual but possible
		"$cmm_composite_name;":         "cmm_composite",
	}
	for in, want := range cases {
		if got := NormalizeCommodity(in); got != want {
			t.Errorf("NormalizeCommodity(%q) = %q, want %q", in, got, want)
		}
	}
}
