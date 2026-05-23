package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CargoFile is the parsed contents of Cargo.json that Frontier writes
// alongside the journal whenever ship cargo changes. The in-journal
// "Cargo" event sometimes inlines the Inventory and sometimes only
// carries a Vessel + Count summary; in the latter case this file is
// the authoritative source of the per-commodity breakdown.
type CargoFile struct {
	Timestamp string         `json:"timestamp"`
	Event     string         `json:"event"` // always "Cargo"
	Vessel    string         `json:"Vessel"`
	Count     int            `json:"Count"`
	Inventory []CargoFileRow `json:"Inventory"`
}

// CargoFileRow is one commodity entry in Cargo.json.
type CargoFileRow struct {
	Name          string `json:"Name"`
	NameLocalised string `json:"Name_Localised"`
	Count         int    `json:"Count"`
	Stolen        int    `json:"Stolen,omitempty"`
	MissionID     int64  `json:"MissionID,omitempty"`
}

// ReadCargoFile parses Cargo.json from the journal directory.
func ReadCargoFile(journalDir string) (*CargoFile, error) {
	path := filepath.Join(journalDir, "Cargo.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Cargo.json: %w", err)
	}
	data = trimUTF8BOM(data)
	var cf CargoFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse Cargo.json: %w", err)
	}
	return &cf, nil
}
