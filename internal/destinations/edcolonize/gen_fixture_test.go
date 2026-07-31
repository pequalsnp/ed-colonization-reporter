package edcolonize

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestGenerateFixture is a helper, not an assertion: run with
// EDCOL_FIXTURE_OUT set to dump a maximally-populated snapshot for use as a
// golden fixture in the receiving repo's tests.
//
//	EDCOL_FIXTURE_OUT=/tmp/snap.json go test -run TestGenerateFixture ./internal/destinations/edcolonize/
func TestGenerateFixture(t *testing.T) {
	out := os.Getenv("EDCOL_FIXTURE_OUT")
	if out == "" {
		t.Skip("set EDCOL_FIXTURE_OUT to regenerate the cross-repo fixture")
	}
	yes := true
	credits := int64(4200000000)
	hull := int64(55000000)
	rebuy := int64(2750000)
	cargoCap := 256
	fuelCap := 32.0
	fuelLvl := 27.4
	jump := 34.2
	fcFuel := 815
	balance := int64(120000000)
	expiry := time.Unix(1700003600, 0).UTC()

	snap := Snapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    time.Unix(1700000000, 0).UTC(),
		Commander:     "Kyle", FID: "F123456",
		GameVersion: "4.1.0.0", GameBuild: "r123/1.0",
		Horizons: &yes, Odyssey: &yes,
		Location: Location{
			StarSystem: "Sol", SystemAddress: 10477373803,
			StarPos: &[3]float64{0, 0, 0}, Body: "Earth",
			Docked: true, StationName: "Abraham Lincoln",
			StationType: "Orbis", MarketID: 128016640,
		},
		Ship: &Ship{
			Type: "python", Name: "Hauler One", Ident: "KG-01",
			HullValue: &hull, Rebuy: &rebuy, CargoCapacity: &cargoCap,
			FuelCapacity: &fuelCap, FuelLevel: &fuelLvl, MaxJumpRange: &jump,
			Modules: []Module{{
				Slot: "FrameShiftDrive", Item: "int_hyperdrive_size5_class5", On: true,
				Blueprint: "FSD_LongRange", Grade: 5, Experiment: "Mass Manager",
			}},
		},
		Credits: &credits,
		Ranks: &Ranks{
			Combat: 5, Trade: 8, Explore: 7, Soldier: 2, Exobiologist: 3, CQC: 1,
			Empire: 12, Federation: 9,
			CombatProgress: 43, TradeProgress: 91, ExploreProgress: 12,
			EmpireProgress: 55, FederationProgress: 20,
		},
		Cargo: []CargoItem{{Name: "steel", LocalisedName: "Steel", Count: 160, Stolen: 0}},
		Materials: &Materials{
			Raw:          []MaterialItem{{Name: "iron", LocalisedName: "Iron", Count: 217}},
			Manufactured: []MaterialItem{{Name: "shieldemitters", Count: 12}},
			Encoded:      []MaterialItem{{Name: "shielddensityreports", Count: 5}},
		},
		Missions: []Mission{{
			MissionID: 900001, Name: "Mission_Delivery", LocalisedName: "Deliver Steel",
			Faction: "Sol Workers", DestinationSystem: "Alpha Centauri",
			DestinationStation: "Hutton Orbital", Commodity: "Steel",
			Count: 340, Reward: 1200000, Expiry: &expiry,
			AcceptedAt: time.Unix(1699999000, 0).UTC(),
		}},
		Colonisation: []Depot{{
			MarketID: 3223343616, StarSystem: "Sol", SystemAddress: 10477373803,
			StationName:          "Construction Site Alpha",
			ConstructionProgress: 0.42,
			Resources: []DepotResource{{
				Name: "steel_name", LocalisedName: "Steel",
				Required: 500, Provided: 160, Payment: 3200,
			}},
			UpdatedAt: time.Unix(1700000000, 0).UTC(),
		}},
		Carriers: []Carrier{{
			MarketID: 3700000000, Name: "Wandering Home", Callsign: "KG1-01T",
			StarSystem: "Sol", SystemAddress: 10477373803,
			FuelLevel: &fcFuel, Balance: &balance,
			Cargo:    []CargoItem{{Name: "tritium", Count: 1200}},
			Services: map[string]any{"refuel": "operational"},
		}},
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %d bytes to %s", len(b), out)
}
