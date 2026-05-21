package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMarketFile(t *testing.T) {
	dir := t.TempDir()
	content := `{
  "timestamp": "2026-05-21T15:00:00Z",
  "event": "Market",
  "MarketID": 3700000123,
  "StationName": "DREAMSTRIDER ABC-12X",
  "StationType": "FleetCarrier",
  "StarSystem": "Sol",
  "Items": [
    {
      "id": 128049177,
      "Name": "$titanium_name;",
      "Name_Localised": "Titanium",
      "Category": "$MARKET_category_metals;",
      "BuyPrice": 0,
      "SellPrice": 0,
      "MeanPrice": 1280,
      "StockBracket": 0,
      "DemandBracket": 0,
      "Stock": 420,
      "Demand": 0,
      "Consumer": false,
      "Producer": true,
      "Rare": false
    },
    {
      "id": 128049178,
      "Name": "$steel_name;",
      "Name_Localised": "Steel",
      "Category": "$MARKET_category_metals;",
      "BuyPrice": 0,
      "SellPrice": 0,
      "MeanPrice": 700,
      "StockBracket": 0,
      "DemandBracket": 0,
      "Stock": 0,
      "Demand": 80,
      "Consumer": true,
      "Producer": false,
      "Rare": false
    }
  ]
}
`
	if err := os.WriteFile(filepath.Join(dir, "Market.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mf, err := ReadMarketFile(dir)
	if err != nil {
		t.Fatalf("ReadMarketFile: %v", err)
	}
	if mf.MarketID != 3700000123 || mf.StationType != "FleetCarrier" {
		t.Errorf("metadata wrong: %+v", mf)
	}
	if len(mf.Items) != 2 {
		t.Fatalf("items len = %d", len(mf.Items))
	}
	if mf.Items[0].Stock != 420 || mf.Items[0].NameLocalised != "Titanium" {
		t.Errorf("first item: %+v", mf.Items[0])
	}
}

func TestReadMarketFile_WithBOM(t *testing.T) {
	dir := t.TempDir()
	content := "\xef\xbb\xbf" + `{"timestamp":"x","event":"Market","MarketID":1,"Items":[]}`
	if err := os.WriteFile(filepath.Join(dir, "Market.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mf, err := ReadMarketFile(dir)
	if err != nil {
		t.Fatalf("ReadMarketFile with BOM: %v", err)
	}
	if mf.MarketID != 1 {
		t.Errorf("MarketID = %d", mf.MarketID)
	}
}

func TestReadMarketFile_Missing(t *testing.T) {
	if _, err := ReadMarketFile(t.TempDir()); err == nil {
		t.Error("expected error when Market.json missing")
	}
}
