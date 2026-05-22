package gui

import (
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
)

func TestFilterProjects(t *testing.T) {
	all := []ravencolonial.Project{
		{BuildID: "1", SystemName: "Sol", BuildName: "Earth Station"},
		{BuildID: "2", SystemName: "Synuefe CN-H d11-83", BuildName: "Belshaw Berth"},
		{BuildID: "3", SystemName: "Alpha Centauri", BuildName: "Hutton Outpost"},
	}
	cases := []struct {
		filter string
		want   []string // BuildIDs expected
	}{
		{"", []string{"1", "2", "3"}},
		{"sol", []string{"1"}},
		{"synuefe", []string{"2"}},
		{"belshaw", []string{"2"}},
		{"OUTpost", []string{"3"}}, // case-insensitive
		{"nope", []string{}},
	}
	for _, c := range cases {
		t.Run(c.filter, func(t *testing.T) {
			got := filterProjects(all, c.filter)
			if len(got) != len(c.want) {
				t.Fatalf("got %d, want %d (filter=%q)", len(got), len(c.want), c.filter)
			}
			for i, p := range got {
				if p.BuildID != c.want[i] {
					t.Errorf("[%d] = %s, want %s", i, p.BuildID, c.want[i])
				}
			}
		})
	}
}
