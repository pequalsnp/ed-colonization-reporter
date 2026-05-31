package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ShipyardFile is the parsed Shipyard.json sibling file the game writes when
// the player opens a station's shipyard. The in-journal "Shipyard" event is
// just the notification that this file is fresh.
type ShipyardFile struct {
	Timestamp      string         `json:"timestamp"`
	Event          string         `json:"event"` // always "Shipyard"
	MarketID       int64          `json:"MarketID"`
	StationName    string         `json:"StationName"`
	StarSystem     string         `json:"StarSystem"`
	Horizons       *bool          `json:"Horizons,omitempty"`
	AllowCobraMkIV *bool          `json:"AllowCobraMkIV,omitempty"`
	PriceList      []ShipyardItem `json:"PriceList"`
}

// ShipyardItem is one purchasable ship. ShipType is the lowercase symbolic
// name (e.g. "sidewinder", "asp", "anaconda").
type ShipyardItem struct {
	ID        int64  `json:"id"`
	ShipType  string `json:"ShipType"`
	ShipPrice int    `json:"ShipPrice"`
}

// ReadShipyardFile parses Shipyard.json from the journal directory.
func ReadShipyardFile(journalDir string) (*ShipyardFile, error) {
	path := filepath.Join(journalDir, "Shipyard.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Shipyard.json: %w", err)
	}
	data = trimUTF8BOM(data)
	var sf ShipyardFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse Shipyard.json: %w", err)
	}
	return &sf, nil
}
