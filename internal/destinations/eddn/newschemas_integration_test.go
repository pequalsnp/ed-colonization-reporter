//go:build integration

// Validates every newly-added EDDN schema against the live gateway's real
// JSON-schema validator via the non-broadcasting `/test` schemaRef path.
//
//	EDDN_INTEGRATION=1 go test -tags integration ./internal/destinations/eddn/...
//
// Skips unless EDDN_INTEGRATION is set. See integration_test.go for the
// rationale and the realTestModeUploader/mustRaw helpers.

package eddn

import (
	"context"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// integrationSession seeds a commander at Sol with live gameversion + coords
// so augmentation-dependent events validate.
func integrationSession() *state.Session {
	sess := state.New()
	sess.SetCommander("edcolreport-integration", "F0")
	sess.SetGameVersion("4.0.0.1903", "r12345/r0 ")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})
	return sess
}

// runAndExpectOK sends raw through a /test-mode uploader and fails unless the
// last status was OK (i.e. the gateway accepted the envelope).
func runAndExpectOK(t *testing.T, sess *state.Session, configure func(*Uploader), raw journal.Raw) {
	t.Helper()
	u := realTestModeUploader(t, sess)
	var lastLevel, lastMsg string
	u.OnStatus = func(level, msg string) {
		lastLevel, lastMsg = level, msg
		t.Logf("EDDN status: %s — %s", level, msg)
	}
	if configure != nil {
		configure(u)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := u.HandleEvent(ctx, raw); err != nil {
		t.Fatalf("HandleEvent rejected: %v (lastStatus=%s %s)", err, lastLevel, lastMsg)
	}
	if lastLevel != "OK" {
		t.Errorf("expected OK from gateway; got %q: %s", lastLevel, lastMsg)
	}
}

func TestIntegration_Scan(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventScan, map[string]any{
		"ScanType":              "Detailed",
		"BodyName":              "Sol 4",
		"BodyID":                5,
		"SystemAddress":         10477373803,
		"DistanceFromArrivalLS": 100.0,
		"WasDiscovered":         true,
		"WasMapped":             false,
	}))
}

func TestIntegration_FSSDiscoveryScan(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventFSSDiscoveryScan, map[string]any{
		"Progress": 0.42, "BodyCount": 21, "NonBodyCount": 9,
		"SystemName": "Sol", "SystemAddress": 10477373803,
	}))
}

func TestIntegration_FSSAllBodiesFound(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventFSSAllBodiesFound, map[string]any{
		"SystemName": "Sol", "SystemAddress": 10477373803, "Count": 30,
	}))
}

func TestIntegration_NavBeaconScan(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventNavBeaconScan, map[string]any{
		"SystemAddress": 10477373803, "NumBodies": 11,
	}))
}

func TestIntegration_ScanBaryCentre(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventScanBaryCentre, map[string]any{
		"SystemAddress": 10477373803, "BodyID": 3,
		"SemiMajorAxis": 1.2e11, "Eccentricity": 0.05, "OrbitalInclination": 0.1,
		"Periapsis": 1.5, "OrbitalPeriod": 9e7, "AscendingNode": 2.0, "MeanAnomaly": 3.0,
	}))
}

func TestIntegration_ApproachSettlement(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventApproachSettlement, map[string]any{
		"Name": "Foo Base", "MarketID": 3500,
		"SystemAddress": 10477373803, "BodyID": 4, "BodyName": "Sol 4",
		"Latitude": 12.5, "Longitude": -30.2,
	}))
}

func TestIntegration_CodexEntry(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventCodexEntry, map[string]any{
		"EntryID": 2100401, "Name": "$Codex_Ent_Stratum_02_Name;", "Name_Localised": "Stratum Tectonicas",
		"Category": "$Codex_Category_Biology;", "SubCategory": "$Codex_SubCategory_Organic_Structures;",
		"Region": "$Codex_RegionName_18;",
		"System": "Sol", "SystemAddress": 10477373803,
		"IsNewEntry": true, "NewTraitsDiscovered": false,
	}))
}

func TestIntegration_SAASignalsFound_JournalAndBodySignals(t *testing.T) {
	requireIntegration(t)
	// SAASignalsFound posts to BOTH journal/1 and fssbodysignals/1; both must
	// validate, so a failure on either flips lastLevel away from OK.
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventSAASignalsFound, map[string]any{
		"SystemAddress": 10477373803, "BodyID": 12, "BodyName": "Sol 4",
		"Signals": []any{
			map[string]any{"Type": "$SAA_SignalType_Biological;", "Type_Localised": "Bio", "Count": 3},
			map[string]any{"Type": "$SAA_SignalType_Geological;", "Count": 1},
		},
	}))
}

func TestIntegration_FSSBodySignals(t *testing.T) {
	requireIntegration(t)
	runAndExpectOK(t, integrationSession(), nil, mustRaw(t, journal.EventFSSBodySignals, map[string]any{
		"SystemAddress": 10477373803, "BodyID": 12, "BodyName": "Sol 4",
		"Signals": []any{map[string]any{"Type": "$SAA_SignalType_Biological;", "Count": 3}},
	}))
}

func TestIntegration_FSSSignalDiscovered(t *testing.T) {
	requireIntegration(t)
	sess := integrationSession()
	u := realTestModeUploader(t, sess)
	var lastLevel, lastMsg string
	u.OnStatus = func(level, msg string) { lastLevel, lastMsg = level, msg; t.Logf("EDDN: %s — %s", level, msg) }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Buffer a signal, then flush it via FSSDiscoveryScan.
	if err := u.HandleEvent(ctx, mustRaw(t, journal.EventFSSSignalDiscovered, map[string]any{
		"SystemAddress": 10477373803, "SignalName": "$MULTIPLAYER_SCENARIO42_TITLE;", "SignalType": "Generic",
	})); err != nil {
		t.Fatalf("buffer signal: %v", err)
	}
	if err := u.HandleEvent(ctx, mustRaw(t, journal.EventFSSDiscoveryScan, map[string]any{
		"BodyCount": 1, "NonBodyCount": 0, "SystemName": "Sol", "SystemAddress": 10477373803,
	})); err != nil {
		t.Fatalf("flush via discovery scan: %v", err)
	}
	if lastLevel != "OK" {
		t.Errorf("expected OK; got %q: %s", lastLevel, lastMsg)
	}
}

func TestIntegration_Outfitting(t *testing.T) {
	requireIntegration(t)
	cfg := func(u *Uploader) {
		u.JournalDir = "/fake"
		u.ReadOutfitting = func(string) (*journal.OutfittingFile, error) {
			return &journal.OutfittingFile{
				MarketID: 3228673, StationName: "Jameson Memorial", StarSystem: "Shinrarta Dezhra",
				Timestamp: "2026-05-21T12:00:00Z",
				Items: []journal.OutfittingItem{
					{Name: "int_engine_size3_class5"},
					{Name: "hpt_pulselaser_fixed_small"},
				},
			}, nil
		}
	}
	runAndExpectOK(t, integrationSession(), cfg, mustRaw(t, journal.EventOutfitting, map[string]any{"MarketID": 3228673}))
}

func TestIntegration_Shipyard(t *testing.T) {
	requireIntegration(t)
	cfg := func(u *Uploader) {
		u.JournalDir = "/fake"
		u.ReadShipyard = func(string) (*journal.ShipyardFile, error) {
			return &journal.ShipyardFile{
				MarketID: 3228673, StationName: "Jameson Memorial", StarSystem: "Shinrarta Dezhra",
				Timestamp: "2026-05-21T12:00:00Z",
				PriceList: []journal.ShipyardItem{{ShipType: "sidewinder"}, {ShipType: "asp"}},
			}, nil
		}
	}
	runAndExpectOK(t, integrationSession(), cfg, mustRaw(t, journal.EventShipyard, map[string]any{"MarketID": 3228673}))
}

func TestIntegration_NavRoute(t *testing.T) {
	requireIntegration(t)
	cfg := func(u *Uploader) {
		u.JournalDir = "/fake"
		u.ReadNavRoute = func(string) (*journal.NavRouteFile, error) {
			return &journal.NavRouteFile{
				Timestamp: "2026-05-21T12:00:00Z",
				Route: []journal.NavRouteEntry{
					{StarSystem: "Sol", SystemAddress: 10477373803, StarPos: [3]float64{0, 0, 0}, StarClass: "G"},
					{StarSystem: "Alpha Centauri", SystemAddress: 12, StarPos: [3]float64{3, 4, 5}, StarClass: "M"},
				},
			}, nil
		}
	}
	runAndExpectOK(t, integrationSession(), cfg, mustRaw(t, journal.EventNavRoute, map[string]any{}))
}
