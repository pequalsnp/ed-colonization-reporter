package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// NavRouteFile is the parsed NavRoute.json sibling file the game writes when
// the player plots a multi-jump route in the galaxy map. The in-journal
// "NavRoute" event is just the notification that this file is fresh.
type NavRouteFile struct {
	Timestamp string          `json:"timestamp"`
	Event     string          `json:"event"` // always "NavRoute"
	Route     []NavRouteEntry `json:"Route"`
}

// NavRouteEntry is one hop of the plotted route.
type NavRouteEntry struct {
	StarSystem    string     `json:"StarSystem"`
	SystemAddress int64      `json:"SystemAddress"`
	StarPos       [3]float64 `json:"StarPos"`
	StarClass     string     `json:"StarClass"`
}

// ReadNavRouteFile parses NavRoute.json from the journal directory.
func ReadNavRouteFile(journalDir string) (*NavRouteFile, error) {
	path := filepath.Join(journalDir, "NavRoute.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read NavRoute.json: %w", err)
	}
	data = trimUTF8BOM(data)
	var nr NavRouteFile
	if err := json.Unmarshal(data, &nr); err != nil {
		return nil, fmt.Errorf("parse NavRoute.json: %w", err)
	}
	return &nr, nil
}
