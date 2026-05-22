package gui

import (
	"strings"
	"testing"
)

func TestSourceTagFor(t *testing.T) {
	cases := []struct {
		msg  string
		want string // the trimmed alphanumeric tag, without padding/brackets
	}{
		{"EDDN: posted journal/1", "EDDN"},
		{"EDDN upload failed: timeout", "EDDN"},
		{"EDSM: 1 event accepted", "EDSM"},
		{"Inara: posted 4 events (4 ok, 0 warn, 0 fail)", "INARA"},
		{"Synced FC LANDMINES4DEMOCRACY from cAPI (3 commodities, 2464 units)", "FC"},
		{"FC cargo delta posted (2 commodity changes)", "FC"},
		{"cAPI /fleetcarrier failed: HTTP 401", "cAPI"},
		{"Reported depot abc-123: 5 commodities outstanding", "RC"},
		{"Created ravencolonial project Belshaw Berth (auto-xyz)", "RC"},
		{"Contributed to abc-123 as Jameson (32 items)", "RC"},
		{"Marked build abc-123 complete", "RC"},
		{"Tailing /path/to/journal", "--"},
		{"some unrelated info", "--"},
	}
	for _, c := range cases {
		t.Run(c.msg, func(t *testing.T) {
			tag, _ := sourceTagFor(c.msg)
			// tag is "[EDDN ]" form; strip non-alnum for comparison
			clean := strings.TrimSpace(strings.Trim(tag, "[]"))
			// Some tags are case-mixed (cAPI); normalise for comparison.
			if !strings.EqualFold(clean, c.want) && clean != c.want {
				t.Errorf("got %q (cleaned %q), want %q", tag, clean, c.want)
			}
		})
	}
}
