package eddn

import (
	"context"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// seededSession returns a session at Sol with a commander + live gameversion,
// so events that need StarSystem/StarPos augmentation can be satisfied.
func seededSession() *state.Session {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.1", "r1/r0 ")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{1, 2, 3})
	return sess
}

func schemasOf(envs []map[string]any) []string {
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		s, _ := e["$schemaRef"].(string)
		out = append(out, s)
	}
	return out
}

func msgOfSchema(t *testing.T, envs []map[string]any, schema string) map[string]any {
	t.Helper()
	for _, e := range envs {
		if e["$schemaRef"] == schema {
			return e["message"].(map[string]any)
		}
	}
	t.Fatalf("no upload with schema %s; got %v", shortSchema(schema), schemasOf(envs))
	return nil
}

func TestUploader_ScanAugmentedToJournal(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)

	raw := mustRaw(t, journal.EventScan, map[string]any{
		"ScanType":           "Detailed",
		"BodyName":           "Sol 4",
		"BodyID":             5,
		"SystemAddress":      10477373803,
		"StarType_Localised": "ignored", // pattern-forbidden, must be stripped
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaJournalV1)
	if msg["StarSystem"] != "Sol" {
		t.Errorf("StarSystem not augmented: %v", msg["StarSystem"])
	}
	if _, ok := msg["StarPos"]; !ok {
		t.Error("StarPos not augmented")
	}
	if _, ok := msg["StarType_Localised"]; ok {
		t.Error("_Localised key not stripped from Scan")
	}
}

func TestUploader_ScanSkippedWhenSystemMismatch(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
	raw := mustRaw(t, journal.EventScan, map[string]any{
		"BodyName":      "Elsewhere 1",
		"SystemAddress": 999999, // not the cached system
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Errorf("expected skip on system mismatch; got %v", schemasOf(captured))
	}
}

func TestUploader_SAASignalsFoundFeedsTwoSchemas(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)

	raw := mustRaw(t, journal.EventSAASignalsFound, map[string]any{
		"SystemAddress": 10477373803,
		"BodyID":        12,
		"BodyName":      "Sol 4",
		"Signals": []any{
			map[string]any{"Type": "$SAA_SignalType_Biological;", "Type_Localised": "Bio", "Count": 3},
			map[string]any{"Type": "$SAA_SignalType_Geological;", "Count": 1},
		},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	// Expect both journal/1 and fssbodysignals/1.
	msgOfSchema(t, captured, schemaJournalV1)
	body := msgOfSchema(t, captured, schemaFSSBodySignalsV1)
	if body["event"] != "FSSBodySignals" {
		t.Errorf("fssbodysignals event = %v, want FSSBodySignals", body["event"])
	}
	sigs := body["Signals"].([]any)
	if len(sigs) != 2 {
		t.Fatalf("Signals len = %d, want 2", len(sigs))
	}
	first := sigs[0].(map[string]any)
	if _, ok := first["Type_Localised"]; ok {
		t.Error("Type_Localised should not be in fssbodysignals signal")
	}
	if first["Type"] != "$SAA_SignalType_Biological;" {
		t.Errorf("signal Type = %v", first["Type"])
	}
}

func TestUploader_FSSDiscoveryScan(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
	raw := mustRaw(t, journal.EventFSSDiscoveryScan, map[string]any{
		"Progress":      0.42, // personal — must NOT appear (allowlist)
		"BodyCount":     21,
		"NonBodyCount":  9,
		"SystemName":    "Sol",
		"SystemAddress": 10477373803,
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaFSSDiscoveryScanV1)
	if _, ok := msg["Progress"]; ok {
		t.Error("Progress must be excluded from fssdiscoveryscan/1")
	}
	for _, k := range []string{"SystemName", "StarPos", "SystemAddress", "BodyCount", "NonBodyCount"} {
		if _, ok := msg[k]; !ok {
			t.Errorf("missing required %q", k)
		}
	}
}

func TestUploader_NavBeaconScanAndScanBaryCentre(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)

	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventNavBeaconScan, map[string]any{
		"SystemAddress": 10477373803,
		"NumBodies":     11,
	})); err != nil {
		t.Fatal(err)
	}
	nb := msgOfSchema(t, captured, schemaNavBeaconScanV1)
	if nb["StarSystem"] != "Sol" || nb["NumBodies"] == nil {
		t.Errorf("navbeaconscan wrong: %v", nb)
	}

	captured = captured[:0]
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventScanBaryCentre, map[string]any{
		"SystemAddress":      10477373803,
		"BodyID":             3,
		"SemiMajorAxis":      1.2e11,
		"Eccentricity":       0.05,
		"OrbitalInclination": 0.1,
	})); err != nil {
		t.Fatal(err)
	}
	bc := msgOfSchema(t, captured, schemaScanBaryCentreV1)
	if bc["BodyID"] == nil || bc["SemiMajorAxis"] == nil || bc["StarSystem"] != "Sol" {
		t.Errorf("scanbarycentre wrong: %v", bc)
	}
}

func TestUploader_ApproachSettlementRequiresLatLong(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)

	// No Latitude/Longitude → skipped.
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventApproachSettlement, map[string]any{
		"Name":          "Foo Base",
		"SystemAddress": 10477373803,
		"BodyID":        4,
		"BodyName":      "Sol 4",
	})); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Errorf("expected skip without lat/long; got %v", schemasOf(captured))
	}

	// With lat/long → uploaded.
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventApproachSettlement, map[string]any{
		"Name":          "Foo Base",
		"SystemAddress": 10477373803,
		"BodyID":        4,
		"BodyName":      "Sol 4",
		"Latitude":      12.5,
		"Longitude":     -30.2,
	})); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaApproachSettlementV1)
	if msg["StarSystem"] != "Sol" || msg["Latitude"] == nil {
		t.Errorf("approachsettlement wrong: %v", msg)
	}
}

func TestUploader_CodexEntryUsesSystemKeyAndDropsPersonal(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
	raw := mustRaw(t, journal.EventCodexEntry, map[string]any{
		"EntryID":             2100401,
		"Name":                "$Codex_Ent_Stratum_02_Name;",
		"Name_Localised":      "Stratum Tectonicas",
		"System":              "Sol",
		"SystemAddress":       10477373803,
		"IsNewEntry":          true, // forbidden
		"NewTraitsDiscovered": true, // forbidden
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaCodexEntryV1)
	if msg["System"] != "Sol" {
		t.Errorf("System = %v", msg["System"])
	}
	for _, k := range []string{"IsNewEntry", "NewTraitsDiscovered", "Name_Localised"} {
		if _, ok := msg[k]; ok {
			t.Errorf("forbidden %q present in codexentry", k)
		}
	}
}

func TestUploader_FSSSignalDiscoveredBatchesAndFlushes(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)

	// Two signals for the cached system; a mission-target USS must be dropped.
	for _, name := range []string{"Sig A", "Sig B"} {
		if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventFSSSignalDiscovered, map[string]any{
			"SystemAddress": 10477373803,
			"SignalName":    name,
		})); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventFSSSignalDiscovered, map[string]any{
		"SystemAddress": 10477373803,
		"SignalName":    "$USS;",
		"USSType":       ussTypeMissionTarget,
	})); err != nil {
		t.Fatal(err)
	}
	// Nothing posted yet — still buffered.
	if len(captured) != 0 {
		t.Fatalf("signals should buffer until flush; got %v", schemasOf(captured))
	}
	// FSSDiscoveryScan flushes the buffer (then posts its own message).
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventFSSDiscoveryScan, map[string]any{
		"BodyCount": 1, "NonBodyCount": 0, "SystemName": "Sol", "SystemAddress": 10477373803,
	})); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaFSSSignalDiscoveredV1)
	sigs := msg["signals"].([]any)
	if len(sigs) != 2 {
		t.Errorf("signals len = %d, want 2 (mission-target dropped)", len(sigs))
	}
}

func TestUploader_FSSSignalFlushesOnSystemChange(t *testing.T) {
	sess := seededSession() // at Sol (addr 10477373803)
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)

	// Buffer a signal in Sol.
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventFSSSignalDiscovered, map[string]any{
		"SystemAddress": 10477373803, "SignalName": "Sol Sig",
	})); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Fatalf("should still be buffered; got %v", schemasOf(captured))
	}
	// Move to a different system and discover a signal there — the Sol buffer
	// must flush before the new system's signal is buffered.
	sess.SetSystemWithPos("Wolf 359", 22, [3]float64{3, 4, 5})
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventFSSSignalDiscovered, map[string]any{
		"SystemAddress": 22, "SignalName": "Wolf Sig",
	})); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaFSSSignalDiscoveredV1)
	if msg["StarSystem"] != "Sol" {
		t.Errorf("flushed buffer should be for Sol; got %v", msg["StarSystem"])
	}
	if sigs := msg["signals"].([]any); len(sigs) != 1 {
		t.Errorf("flushed signals = %d, want 1", len(sigs))
	}
}

func TestUploader_ExplorationSkippedWithoutLocation(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.1", "r1/r0 ") // no SetSystemWithPos → no coords cached
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)

	// FSSDiscoveryScan carries SystemName but not StarPos; with no cached
	// coords to augment from, it must be skipped (not sent malformed).
	if err := u.HandleEvent(context.Background(), mustRaw(t, journal.EventFSSDiscoveryScan, map[string]any{
		"BodyCount": 1, "NonBodyCount": 0, "SystemName": "Sol", "SystemAddress": 10477373803,
	})); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Errorf("expected skip without cached StarPos; got %v", schemasOf(captured))
	}
}

func TestUploader_OutfittingFiltersModules(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
	u.JournalDir = "/fake"
	u.ReadOutfitting = func(string) (*journal.OutfittingFile, error) {
		return &journal.OutfittingFile{
			MarketID: 3228673, StationName: "Jameson Memorial", StarSystem: "Shinrarta Dezhra",
			Timestamp: "2026-05-21T12:00:00Z",
			Items: []journal.OutfittingItem{
				{Name: "int_engine_size3_class5"},
				{Name: "hpt_pulselaser_fixed_small"},
				{Name: "int_corrosionproofcargorack_size1_class1"}, // matches Int_
				{Name: "decal_explorer"},                           // not a module — filtered
				{Name: "paintjob_anaconda_default"},                // filtered
				{Name: ""},                                         // filtered
			},
		}, nil
	}
	raw := mustRaw(t, journal.EventOutfitting, map[string]any{"MarketID": 3228673})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaOutfittingV2)
	mods := msg["modules"].([]any)
	if len(mods) != 3 {
		t.Errorf("modules = %v, want 3 (decals/paintjobs filtered)", mods)
	}
	if msg["systemName"] != "Shinrarta Dezhra" || msg["stationName"] != "Jameson Memorial" {
		t.Errorf("station identity wrong: %v", msg)
	}
}

func TestUploader_OutfittingSkipsStaleFile(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
	u.JournalDir = "/fake"
	u.ReadOutfitting = func(string) (*journal.OutfittingFile, error) {
		return &journal.OutfittingFile{MarketID: 111, StationName: "S", StarSystem: "Sys",
			Items: []journal.OutfittingItem{{Name: "int_engine_size3_class5"}}}, nil
	}
	raw := mustRaw(t, journal.EventOutfitting, map[string]any{"MarketID": 999}) // mismatch
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Errorf("stale Outfitting.json should be skipped; got %v", schemasOf(captured))
	}
}

func TestUploader_Shipyard(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
	u.JournalDir = "/fake"
	cobra := true
	u.ReadShipyard = func(string) (*journal.ShipyardFile, error) {
		return &journal.ShipyardFile{
			MarketID: 42, StationName: "Dock", StarSystem: "Sol", Timestamp: "2026-05-21T12:00:00Z",
			AllowCobraMkIV: &cobra,
			PriceList: []journal.ShipyardItem{
				{ShipType: "sidewinder"}, {ShipType: "asp"}, {ShipType: "sidewinder"}, // dup collapsed
			},
		}, nil
	}
	raw := mustRaw(t, journal.EventShipyard, map[string]any{"MarketID": 42})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaShipyardV2)
	ships := msg["ships"].([]any)
	if len(ships) != 2 {
		t.Errorf("ships = %v, want 2 unique", ships)
	}
	if msg["allowCobraMkIV"] != true {
		t.Errorf("allowCobraMkIV = %v", msg["allowCobraMkIV"])
	}
}

func TestUploader_NavRoute(t *testing.T) {
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, seededSession(), &captured)
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
	raw := mustRaw(t, journal.EventNavRoute, map[string]any{})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	msg := msgOfSchema(t, captured, schemaNavRouteV1)
	route := msg["Route"].([]any)
	if len(route) != 2 {
		t.Fatalf("Route len = %d, want 2", len(route))
	}
	first := route[0].(map[string]any)
	for _, k := range []string{"StarSystem", "SystemAddress", "StarPos", "StarClass"} {
		if _, ok := first[k]; !ok {
			t.Errorf("route entry missing %q", k)
		}
	}
}
