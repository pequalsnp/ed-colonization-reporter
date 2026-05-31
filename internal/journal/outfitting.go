package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// OutfittingFile is the parsed Outfitting.json sibling file the game writes
// when the player opens a station's outfitting menu. The in-journal
// "Outfitting" event is just the notification that this file is fresh.
type OutfittingFile struct {
	Timestamp   string           `json:"timestamp"`
	Event       string           `json:"event"` // always "Outfitting"
	MarketID    int64            `json:"MarketID"`
	StationName string           `json:"StationName"`
	StarSystem  string           `json:"StarSystem"`
	Horizons    *bool            `json:"Horizons,omitempty"`
	Items       []OutfittingItem `json:"Items"`
}

// OutfittingItem is one purchasable module. Name is the lowercase symbolic
// name (e.g. "int_engine_size3_class5", "hpt_pulselaser_fixed_small").
type OutfittingItem struct {
	ID       int64  `json:"id"`
	Name     string `json:"Name"`
	BuyPrice int    `json:"BuyPrice"`
}

// ReadOutfittingFile parses Outfitting.json from the journal directory.
func ReadOutfittingFile(journalDir string) (*OutfittingFile, error) {
	path := filepath.Join(journalDir, "Outfitting.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Outfitting.json: %w", err)
	}
	data = trimUTF8BOM(data)
	var of OutfittingFile
	if err := json.Unmarshal(data, &of); err != nil {
		return nil, fmt.Errorf("parse Outfitting.json: %w", err)
	}
	return &of, nil
}
