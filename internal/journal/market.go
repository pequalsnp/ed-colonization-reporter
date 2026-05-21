package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MarketFile is the parsed contents of the Market.json sibling file that
// Frontier writes alongside the journal. The full commodities inventory of
// the station the player most recently interacted with lives here — the
// in-journal "Market" event is just a notification that this file is fresh.
type MarketFile struct {
	Timestamp   string       `json:"timestamp"`
	Event       string       `json:"event"` // always "Market"
	MarketID    int64        `json:"MarketID"`
	StationName string       `json:"StationName"`
	StationType string       `json:"StationType"`
	StarSystem  string       `json:"StarSystem"`
	Items       []MarketItem `json:"Items"`
}

// MarketItem is one commodity entry.
type MarketItem struct {
	ID             int64  `json:"id"`
	Name           string `json:"Name"`
	NameLocalised  string `json:"Name_Localised"`
	Category       string `json:"Category"`
	BuyPrice       int    `json:"BuyPrice"`
	SellPrice      int    `json:"SellPrice"`
	MeanPrice      int    `json:"MeanPrice"`
	StockBracket   int    `json:"StockBracket"`
	DemandBracket  int    `json:"DemandBracket"`
	Stock          int    `json:"Stock"`
	Demand         int    `json:"Demand"`
	Consumer       bool   `json:"Consumer"`
	Producer       bool   `json:"Producer"`
	Rare           bool   `json:"Rare"`
}

// ReadMarketFile parses Market.json from the journal directory.
func ReadMarketFile(journalDir string) (*MarketFile, error) {
	path := filepath.Join(journalDir, "Market.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Market.json: %w", err)
	}
	data = trimUTF8BOM(data)
	var mf MarketFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parse Market.json: %w", err)
	}
	return &mf, nil
}
