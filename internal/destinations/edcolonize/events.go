package edcolonize

// Journal event shapes this destination needs that internal/journal doesn't
// already define. They live here rather than in internal/journal because
// nothing else in the repo consumes them — the outward destinations don't
// report materials, ranks, or the mission list.

import "time"

// materialsEvent is the "Materials" journal event, emitted once at session
// start with the commander's full engineering inventory.
type materialsEvent struct {
	Raw          []materialRow `json:"Raw"`
	Manufactured []materialRow `json:"Manufactured"`
	Encoded      []materialRow `json:"Encoded"`
}

type materialRow struct {
	Name          string `json:"Name"`
	NameLocalised string `json:"Name_Localised"`
	Count         int    `json:"Count"`
}

// rankEvent covers both "Rank" (absolute rank indices) and "Progress"
// (percent toward the next rank). Identical field names, different meaning —
// the caller decides which set to populate.
type rankEvent struct {
	Combat       int `json:"Combat"`
	Trade        int `json:"Trade"`
	Explore      int `json:"Explore"`
	Soldier      int `json:"Soldier"`
	Exobiologist int `json:"Exobiologist"`
	CQC          int `json:"CQC"`
	Empire       int `json:"Empire"`
	Federation   int `json:"Federation"`
}

// missionAcceptedEvent is the "MissionAccepted" journal event. Frontier only
// emits the commodity/count fields for delivery-type missions.
type missionAcceptedEvent struct {
	MissionID          int64     `json:"MissionID"`
	Name               string    `json:"Name"`
	LocalisedName      string    `json:"LocalisedName"`
	Faction            string    `json:"Faction"`
	DestinationSystem  string    `json:"DestinationSystem"`
	DestinationStation string    `json:"DestinationStation"`
	Commodity          string    `json:"Commodity"`
	CommodityLocalised string    `json:"Commodity_Localised"`
	Count              int       `json:"Count"`
	Reward             int64     `json:"Reward"`
	Expiry             time.Time `json:"Expiry"`
}

// missionsEvent is the "Missions" journal event, emitted at session start
// with the authoritative active/failed/complete lists. We only need Active.
type missionsEvent struct {
	Active []struct {
		MissionID     int64  `json:"MissionID"`
		Name          string `json:"Name"`
		LocalisedName string `json:"LocalisedName"`
	} `json:"Active"`
}

// loadoutEvent is the subset of the "Loadout" journal event we summarise.
// The Session stores the raw payload; we re-parse it here rather than
// widening state.Session with fields only edcolonize cares about.
type loadoutEvent struct {
	Ship          string          `json:"Ship"`
	ShipName      string          `json:"ShipName"`
	ShipIdent     string          `json:"ShipIdent"`
	HullValue     *int64          `json:"HullValue"`
	Rebuy         *int64          `json:"Rebuy"`
	CargoCapacity *int            `json:"CargoCapacity"`
	MaxJumpRange  *float64        `json:"MaxJumpRange"`
	FuelCapacity  *fuelCapacity   `json:"FuelCapacity"`
	Modules       []loadoutModule `json:"Modules"`
}

type fuelCapacity struct {
	Main    float64 `json:"Main"`
	Reserve float64 `json:"Reserve"`
}

type loadoutModule struct {
	Slot        string `json:"Slot"`
	Item        string `json:"Item"`
	On          bool   `json:"On"`
	Engineering *struct {
		BlueprintName               string `json:"BlueprintName"`
		Level                       int    `json:"Level"`
		ExperimentalEffectLocalised string `json:"ExperimentalEffect_Localised"`
		ExperimentalEffect          string `json:"ExperimentalEffect"`
	} `json:"Engineering"`
}
