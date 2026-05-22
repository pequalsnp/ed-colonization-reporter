package reporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func TestLevel_String(t *testing.T) {
	cases := map[Level]string{
		LevelInfo:  "INFO",
		LevelOK:    "OK",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
		Level(99):  "INFO", // unknown falls back to INFO
	}
	for lvl, want := range cases {
		if got := lvl.String(); got != want {
			t.Errorf("Level(%d).String() = %q, want %q", lvl, got, want)
		}
	}
}

func TestReporter_Name(t *testing.T) {
	r := New(&fakeAPI{}, state.New())
	if r.Name() != "ravencolonial" {
		t.Errorf("Name = %q, want ravencolonial", r.Name())
	}
}

func TestHandleCarrierStats_RegistersAndPublishes(t *testing.T) {
	sess := state.New()
	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventCarrierStats, map[string]any{
		"CarrierID":     3700000123,
		"Callsign":      "ABC-12X",
		"Name":          "DREAMSTRIDER",
		"DockingAccess": "all",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !sess.IsOwnedCarrier(3700000123) {
		t.Error("CarrierStats should register owned carrier")
	}
	if len(api.fcPuts) != 1 {
		t.Fatalf("fcPuts = %d", len(api.fcPuts))
	}
	put := api.fcPuts[0]
	if put.Callsign != "ABC-12X" || put.Name != "DREAMSTRIDER" || put.MarketID != 3700000123 {
		t.Errorf("PutFleetCarrier got %+v", put)
	}
}

func TestHandleCarrierStats_SilentWhenNoAPIKey(t *testing.T) {
	sess := state.New()
	api := &fakeAPI{fcPutErr: ravencolonial.ErrNoAPIKey}
	r := New(api, sess)
	statusCount := 0
	r.OnStatus(func(s Status) {
		if s.Level == LevelError {
			statusCount++
		}
	})
	raw := mustRaw(t, journal.EventCarrierStats, map[string]any{
		"CarrierID": 3700000123, "Callsign": "ABC-12X", "Name": "X",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !sess.IsOwnedCarrier(3700000123) {
		t.Error("session should still register the carrier even without API key")
	}
	if statusCount != 0 {
		t.Errorf("expected no error statuses for missing API key; got %d", statusCount)
	}
}

func TestHandleCarrierLocation_UpdatesSystem(t *testing.T) {
	sess := state.New()
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventCarrierLocation, map[string]any{
		"CarrierID": 42, "StarSystem": "Sol", "SystemAddress": 100,
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	c, _ := sess.OwnedCarrier(42)
	if c.StarSystem != "Sol" || c.SystemAddress != 100 || c.Callsign != "Y" {
		t.Errorf("merged carrier: %+v", c)
	}
}

func TestHandleMarket_SyncsCargoForOwnedFC(t *testing.T) {
	sess := state.New()
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 3700000123, Name: "X", Callsign: "Y"})
	api := &fakeAPI{}
	r := New(api, sess)
	r.JournalDir = "/fake" // exists only via the override below
	r.readMarketFile = func(dir string) (*journal.MarketFile, error) {
		return &journal.MarketFile{
			MarketID: 3700000123,
			Items: []journal.MarketItem{
				{Name: "$titanium_name;", Stock: 420},
				{Name: "$steel_name;", Stock: 100},
				{Name: "$gold_name;", Stock: 0}, // should be dropped
			},
		}, nil
	}
	raw := mustRaw(t, journal.EventMarket, map[string]any{
		"MarketID":    3700000123,
		"StationType": "FleetCarrier",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargo) != 1 {
		t.Fatalf("fcCargo = %d", len(api.fcCargo))
	}
	call := api.fcCargo[0]
	if call.MarketID != 3700000123 {
		t.Errorf("market = %d", call.MarketID)
	}
	want := ravencolonial.Cargo{"titanium": 420, "steel": 100}
	if !reflect.DeepEqual(call.Cargo, want) {
		t.Errorf("cargo = %+v, want %+v", call.Cargo, want)
	}
}

func TestHandleMarket_SkipsNonOwnedFC(t *testing.T) {
	sess := state.New()
	api := &fakeAPI{}
	r := New(api, sess)
	r.JournalDir = "/fake"
	r.readMarketFile = func(dir string) (*journal.MarketFile, error) {
		t.Fatal("should not be called for non-owned market")
		return nil, nil
	}
	raw := mustRaw(t, journal.EventMarket, map[string]any{
		"MarketID":    99,
		"StationType": "FleetCarrier",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargo) != 0 {
		t.Errorf("should not have synced for non-owned market")
	}
}

func TestHandleMarket_SkipsStaleMarketFile(t *testing.T) {
	sess := state.New()
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	api := &fakeAPI{}
	r := New(api, sess)
	r.JournalDir = "/fake"
	r.readMarketFile = func(dir string) (*journal.MarketFile, error) {
		return &journal.MarketFile{MarketID: 99, Items: nil}, nil // wrong market!
	}
	raw := mustRaw(t, journal.EventMarket, map[string]any{
		"MarketID":    42,
		"StationType": "FleetCarrier",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargo) != 0 {
		t.Errorf("should not have synced with stale Market.json")
	}
}

func TestHandleMarket_SilentWhenNoAPIKey(t *testing.T) {
	sess := state.New()
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	api := &fakeAPI{fcCargoErr: ravencolonial.ErrNoAPIKey}
	r := New(api, sess)
	r.JournalDir = "/fake"
	r.readMarketFile = func(dir string) (*journal.MarketFile, error) {
		return &journal.MarketFile{MarketID: 42, Items: []journal.MarketItem{{Name: "$titanium_name;", Stock: 1}}}, nil
	}
	errCount := 0
	r.OnStatus(func(s Status) {
		if s.Level == LevelError {
			errCount++
		}
	})
	raw := mustRaw(t, journal.EventMarket, map[string]any{
		"MarketID":    42,
		"StationType": "FleetCarrier",
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if errCount != 0 {
		t.Errorf("expected no error status for missing key, got %d", errCount)
	}
}

func TestHandleCarrierJump_PreservesOwnedRecord(t *testing.T) {
	sess := state.New()
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "MY-FC", Callsign: "ABC-12X"})
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventCarrierJump, map[string]any{
		"Docked":        true,
		"StationName":   "MY-FC ABC-12X",
		"StationType":   "FleetCarrier",
		"MarketID":      42,
		"StarSystem":    "Alpha Centauri",
		"SystemAddress": 1234567,
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	// Should update player's system AND merge into owned record.
	name, addr := sess.System()
	if name != "Alpha Centauri" || addr != 1234567 {
		t.Errorf("player system = (%q, %d)", name, addr)
	}
	c, _ := sess.OwnedCarrier(42)
	if c.StarSystem != "Alpha Centauri" || c.Name != "MY-FC" {
		t.Errorf("owned carrier merge wrong: %+v", c)
	}
}

func TestHandleCargoTransfer_PatchesDeltaForOwnedFC(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 3700000123, Name: "MY-FC", Callsign: "ABC-12X"})
	sess.SetDocked("MY-FC ABC-12X", 3700000123, 100)

	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventCargoTransfer, map[string]any{
		"Transfers": []map[string]any{
			{"Type": "cmmcomposite", "Type_Localised": "CMM Composite", "Count": 2464, "Direction": "tocarrier"},
			{"Type": "titanium", "Type_Localised": "Titanium", "Count": 100, "Direction": "toship"},
			{"Type": "limpet", "Count": 8, "Direction": "tosrv"}, // must be ignored
		},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargoPatches) != 1 {
		t.Fatalf("fcCargoPatches = %d, want 1", len(api.fcCargoPatches))
	}
	patch := api.fcCargoPatches[0]
	if patch.MarketID != 3700000123 {
		t.Errorf("MarketID = %d", patch.MarketID)
	}
	if patch.Cargo["cmmcomposite"] != 2464 {
		t.Errorf("cmmcomposite delta = %d, want +2464", patch.Cargo["cmmcomposite"])
	}
	if patch.Cargo["titanium"] != -100 {
		t.Errorf("titanium delta = %d, want -100", patch.Cargo["titanium"])
	}
	if _, ok := patch.Cargo["limpet"]; ok {
		t.Error("tosrv direction must not produce an FC delta")
	}
}

func TestHandleCargoTransfer_SkipsWhenNotAtOwnedFC(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	// Docked at a station that is NOT our FC.
	sess.SetDocked("Abraham Lincoln", 128666761, 100)

	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventCargoTransfer, map[string]any{
		"Transfers": []map[string]any{{"Type": "titanium", "Count": 100, "Direction": "toship"}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargoPatches) != 0 {
		t.Errorf("transfer outside of owned FC must not PATCH; got %v", api.fcCargoPatches)
	}
}

func TestHandleCargoTransfer_SkipsWhenUndocked(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	// undocked

	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventCargoTransfer, map[string]any{
		"Transfers": []map[string]any{{"Type": "titanium", "Count": 5, "Direction": "tocarrier"}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargoPatches) != 0 {
		t.Errorf("transfer while undocked must not PATCH; got %v", api.fcCargoPatches)
	}
}

func TestHandleCargoTransfer_SkipsOnReplayed(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	sess.SetDocked("X Y", 42, 100)
	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventCargoTransfer, map[string]any{
		"Transfers": []map[string]any{{"Type": "cmmcomposite", "Count": 2464, "Direction": "tocarrier"}},
	})
	raw.Replayed = true // backfill — must NOT PATCH again
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargoPatches) != 0 {
		t.Errorf("replayed CargoTransfer must not PATCH; got %v", api.fcCargoPatches)
	}
}

func TestHandleContribution_SkipsOnReplayed(t *testing.T) {
	sess := setupSession()
	sess.RememberBuild(3789012345, "build-1")
	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationContribution, map[string]any{
		"MarketID":      3789012345,
		"Contributions": []map[string]any{{"Name": "$titanium_name;", "Amount": 32}},
	})
	raw.Replayed = true // backfill — must NOT double-credit
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.contributions) != 0 {
		t.Errorf("replayed contribution must not POST; got %v", api.contributions)
	}
}

func TestHandleDepot_StillProcessesOnReplayed(t *testing.T) {
	// Depot updates are idempotent — they overwrite the server state with
	// the absolute outstanding-needs map — so they're safe to re-fire on
	// backfill. This guards against accidentally extending the replay
	// skip to handlers that don't need it.
	sess := setupSession()
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-1"},
		},
	}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":          3789012345,
		"ResourcesRequired": []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 1000, "ProvidedAmount": 400}},
	})
	raw.Replayed = true
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.updates) != 1 {
		t.Errorf("idempotent depot update must still fire on replay; got %d", len(api.updates))
	}
}

func TestHandleCargoTransfer_AggregatesMultipleRowsSameCommodity(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	sess.SetDocked("X Y", 42, 100)

	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventCargoTransfer, map[string]any{
		"Transfers": []map[string]any{
			{"Type": "titanium", "Count": 50, "Direction": "tocarrier"},
			{"Type": "titanium", "Count": 30, "Direction": "tocarrier"},
		},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.fcCargoPatches) != 1 {
		t.Fatalf("got %d patches", len(api.fcCargoPatches))
	}
	if api.fcCargoPatches[0].Cargo["titanium"] != 80 {
		t.Errorf("titanium delta = %d, want 80", api.fcCargoPatches[0].Cargo["titanium"])
	}
}

func TestHandleCargoTransfer_SilentWhenNoAPIKey(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.RegisterOwnedCarrier(state.OwnedCarrier{MarketID: 42, Name: "X", Callsign: "Y"})
	sess.SetDocked("X", 42, 100)
	api := &fakeAPI{fcCargoErr: ravencolonial.ErrNoAPIKey}
	r := New(api, sess)
	errs := 0
	r.OnStatus(func(s Status) {
		if s.Level == LevelError {
			errs++
		}
	})
	raw := mustRaw(t, journal.EventCargoTransfer, map[string]any{
		"Transfers": []map[string]any{{"Type": "titanium", "Count": 5, "Direction": "tocarrier"}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if errs != 0 {
		t.Errorf("missing API key should not surface ERROR status; got %d", errs)
	}
}

func TestHandleCarrierJump_DoesNotClaimOwnershipOfOtherFC(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventCarrierJump, map[string]any{
		"Docked":        true,
		"MarketID":      999, // not ours
		"StarSystem":    "Beta",
		"SystemAddress": 1,
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if sess.IsOwnedCarrier(999) {
		t.Error("docking on someone else's FC must not register it as owned")
	}
}

// fakeAPI is a minimal stub of the ravencolonial APIClient used to assert
// what the reporter calls.
type fakeAPI struct {
	lookups        []lookupCall
	updates        []ravencolonial.ProjectUpdate
	creates        []ravencolonial.ProjectCreate
	completes      []string
	contributions  []contributionCall
	fcPuts         []ravencolonial.FleetCarrier
	fcCargo        []fcCargoCall
	fcCargoPatches []fcCargoCall
	lookupResp     map[lookupKey]*ravencolonial.Project
	lookupErr      map[lookupKey]error
	createResp     *ravencolonial.Project
	createErr      error
	updateErr      error
	fcPutErr       error
	fcCargoErr     error
}

type fcCargoCall struct {
	MarketID int64
	Cargo    ravencolonial.Cargo
}

type lookupCall struct {
	SystemAddress int64
	MarketID      int64
}

type lookupKey struct {
	sys, mkt int64
}

type contributionCall struct {
	BuildID string
	Cmdr    string
	Contrib ravencolonial.Contribution
}

func (f *fakeAPI) ProjectBySystemMarket(_ context.Context, sysAddr, marketID int64) (*ravencolonial.Project, error) {
	f.lookups = append(f.lookups, lookupCall{sysAddr, marketID})
	k := lookupKey{sysAddr, marketID}
	if err, ok := f.lookupErr[k]; ok {
		return nil, err
	}
	if p, ok := f.lookupResp[k]; ok {
		return p, nil
	}
	return nil, &ravencolonial.APIError{StatusCode: http.StatusNotFound, Status: "404", URL: "test"}
}

func (f *fakeAPI) UpdateProject(_ context.Context, u ravencolonial.ProjectUpdate) error {
	f.updates = append(f.updates, u)
	return f.updateErr
}

func (f *fakeAPI) CreateProject(_ context.Context, p ravencolonial.ProjectCreate) (*ravencolonial.Project, error) {
	f.creates = append(f.creates, p)
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createResp != nil {
		return f.createResp, nil
	}
	// Synthesize a reasonable default: build ID = "auto-" + market.
	return &ravencolonial.Project{
		BuildID:       fmt.Sprintf("auto-%d", p.MarketID),
		BuildName:     p.BuildName,
		SystemName:    p.SystemName,
		SystemAddress: p.SystemAddress,
		MarketID:      p.MarketID,
		MaxNeed:       p.MaxNeed,
		Commodities:   p.Commodities,
	}, nil
}

func (f *fakeAPI) CompleteProject(_ context.Context, buildID string) error {
	f.completes = append(f.completes, buildID)
	return nil
}

func (f *fakeAPI) Contribute(_ context.Context, buildID, cmdr string, c ravencolonial.Contribution) error {
	f.contributions = append(f.contributions, contributionCall{buildID, cmdr, c})
	return nil
}

func (f *fakeAPI) PutFleetCarrier(_ context.Context, fc ravencolonial.FleetCarrier) error {
	f.fcPuts = append(f.fcPuts, fc)
	return f.fcPutErr
}

func (f *fakeAPI) OverwriteCarrierCargo(_ context.Context, marketID int64, c ravencolonial.Cargo) error {
	f.fcCargo = append(f.fcCargo, fcCargoCall{MarketID: marketID, Cargo: c})
	return f.fcCargoErr
}

func (f *fakeAPI) PatchCarrierCargo(_ context.Context, marketID int64, c ravencolonial.Cargo) error {
	f.fcCargoPatches = append(f.fcCargoPatches, fcCargoCall{MarketID: marketID, Cargo: c})
	return f.fcCargoErr
}

func mustRaw(t *testing.T, event string, payload any) journal.Raw {
	t.Helper()
	// Marshal into a generic map first so we can inject the event key.
	pm, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(pm, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	m["event"] = event
	if _, ok := m["timestamp"]; !ok {
		m["timestamp"] = "2026-05-21T12:00:00Z"
	}
	line, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	raw, err := journal.ParseLine(line)
	if err != nil {
		t.Fatalf("parseline: %v", err)
	}
	return raw
}

func setupSession() *state.Session {
	s := state.New()
	s.SetCommander("Jameson", "F1")
	s.SetSystem("Sol", 10477373803)
	s.SetDocked("$EXT_PNL_ColonisationShip:#index=1;", 3789012345, 10477373803)
	return s
}

func TestHandleDepot_LooksUpAndUpdates(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-1", SystemName: "Sol"},
		},
	}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":             3789012345,
		"ConstructionProgress": 0.5,
		"ConstructionComplete": false,
		"ResourcesRequired": []map[string]any{
			{"Name": "$titanium_name;", "Name_Localised": "Titanium", "RequiredAmount": 1000, "ProvidedAmount": 400},
			{"Name": "$steel_name;", "Name_Localised": "Steel", "RequiredAmount": 500, "ProvidedAmount": 500},
		},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.updates) != 1 {
		t.Fatalf("updates = %d", len(api.updates))
	}
	u := api.updates[0]
	if u.BuildID != "build-1" {
		t.Errorf("buildId = %q", u.BuildID)
	}
	want := map[string]int{"titanium": 600, "steel": 0}
	if !reflect.DeepEqual(u.Commodities, want) {
		t.Errorf("commodities = %+v, want %+v", u.Commodities, want)
	}
	// MaxNeed = sum of all RequiredAmount (1000 + 500), matching SrvSurvey.
	if u.MaxNeed != 1500 {
		t.Errorf("maxNeed = %d, want 1500 (sum of RequiredAmount)", u.MaxNeed)
	}
	if got, _ := sess.BuildFor(3789012345); got != "build-1" {
		t.Errorf("buildId not cached: %q", got)
	}
	if len(api.completes) != 0 {
		t.Errorf("did not expect complete call")
	}
}

func TestHandleDepot_CachesBuildIDAcrossEvents(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-1"},
		},
	}
	r := New(api, sess)
	depot := func() journal.Raw {
		return mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
			"MarketID":             3789012345,
			"ConstructionProgress": 0.5,
			"ResourcesRequired":    []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 100, "ProvidedAmount": 10}},
		})
	}
	if err := r.HandleEvent(context.Background(), depot()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := r.HandleEvent(context.Background(), depot()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(api.lookups) != 1 {
		t.Errorf("lookups = %d, want 1 (second event should hit cache)", len(api.lookups))
	}
	if len(api.updates) != 2 {
		t.Errorf("updates = %d, want 2", len(api.updates))
	}
}

func TestHandleDepot_NotFoundTriggersCreate(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{} // empty lookupResp → 404 from fake; createResp nil → synthesised
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":          3789012345,
		"ResourcesRequired": []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 1000, "ProvidedAmount": 0}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.creates) != 1 {
		t.Fatalf("expected one CreateProject call; got %d", len(api.creates))
	}
	p := api.creates[0]
	if p.MarketID != 3789012345 || p.SystemAddress != 10477373803 || p.SystemName != "Sol" {
		t.Errorf("create payload identity wrong: %+v", p)
	}
	if p.Commodities["titanium"] != 1000 {
		t.Errorf("create outstanding titanium = %d, want 1000", p.Commodities["titanium"])
	}
	if p.MaxNeed != 1000 {
		t.Errorf("create MaxNeed = %d, want 1000", p.MaxNeed)
	}
	if p.ArchitectName != "Jameson" {
		t.Errorf("ArchitectName = %q, want Jameson", p.ArchitectName)
	}
	if cmds := p.Commanders["Jameson"]; cmds == nil {
		t.Errorf("Commanders map should seed the architect: %+v", p.Commanders)
	}
	// The fake synthesises a buildId from the market id.
	gotID, _ := sess.BuildFor(3789012345)
	if gotID != "auto-3789012345" {
		t.Errorf("session did not cache new buildId; got %q", gotID)
	}
	// No regular UpdateProject — create already POSTed the snapshot.
	if len(api.updates) != 0 {
		t.Errorf("expected zero post-create updates; got %d", len(api.updates))
	}
}

func TestHandleDepot_CreatedPrimaryPort(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})
	// Docked at a colonisation ship — should be flagged as primary port.
	sess.SetDocked("$EXT_PNL_ColonisationShip:#index=1;", 3789012345, 10477373803)

	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":          3789012345,
		"ResourcesRequired": []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 100, "ProvidedAmount": 0}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.creates) != 1 {
		t.Fatalf("creates = %d", len(api.creates))
	}
	p := api.creates[0]
	if !p.IsPrimaryPort {
		t.Error("IsPrimaryPort should be true for a colonisation-ship dock")
	}
	if p.BuildName != "Primary port" {
		t.Errorf("BuildName = %q, want Primary port", p.BuildName)
	}
	if p.BuildType != "primary-port" {
		t.Errorf("BuildType = %q, want primary-port", p.BuildType)
	}
}

func TestHandleDepot_CreateSkippedWithoutCommander(t *testing.T) {
	sess := state.New()
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})
	sess.SetDocked("X", 3789012345, 10477373803)
	// No commander set.
	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":          3789012345,
		"ResourcesRequired": []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 100, "ProvidedAmount": 0}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.creates) != 0 {
		t.Errorf("create should be skipped without a commander; got %d", len(api.creates))
	}
}

func TestDeriveBuildName(t *testing.T) {
	cases := map[string]string{
		// Colonisation-ship variants — SrvSurvey collapses all of them
		// to "Primary port" regardless of any appended text.
		"System Colonisation Ship":              "Primary port",
		"$EXT_PANEL_ColonisationShip; foo":      "Primary port",
		"$EXT_PANEL_ColonisationShip:#index=1;": "Primary port",
		"$EXT_PNL_ColonisationShip:#index=1;":   "Primary port",
		// Regular construction sites — strip the prefix, trim.
		"Orbital Construction Site: Belshaw Berth":   "Belshaw Berth",
		"Planetary Construction Site: Belshaw Berth": "Belshaw Berth",
		"Some Random Station":                        "Some Random Station",
		"":                                           "",
	}
	for in, want := range cases {
		if got := deriveBuildName(in); got != want {
			t.Errorf("deriveBuildName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleDepot_CompleteTriggersComplete(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-1"},
		},
	}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":             3789012345,
		"ConstructionProgress": 1.0,
		"ConstructionComplete": true,
		"ResourcesRequired":    []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 100, "ProvidedAmount": 100}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.completes) != 1 || api.completes[0] != "build-1" {
		t.Errorf("completes = %v", api.completes)
	}
}

func TestHandleDepot_PropagatesUpdateError(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-1"},
		},
		updateErr: errors.New("boom"),
	}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":          3789012345,
		"ResourcesRequired": []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 100, "ProvidedAmount": 0}},
	})
	if err := r.HandleEvent(context.Background(), raw); err == nil {
		t.Error("expected error to propagate")
	}
}

func TestHandleContribution_UsesCachedBuildID(t *testing.T) {
	sess := setupSession()
	sess.RememberBuild(3789012345, "build-cached")
	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationContribution, map[string]any{
		"MarketID": 3789012345,
		"Contributions": []map[string]any{
			{"Name": "$titanium_name;", "Name_Localised": "Titanium", "Amount": 32},
			{"Name": "$steel_name;", "Amount": 10},
		},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.lookups) != 0 {
		t.Errorf("did not expect lookup; cache should win")
	}
	if len(api.contributions) != 1 {
		t.Fatalf("contributions = %d", len(api.contributions))
	}
	c := api.contributions[0]
	if c.BuildID != "build-cached" || c.Cmdr != "Jameson" {
		t.Errorf("got %+v", c)
	}
	if c.Contrib["titanium"] != 32 || c.Contrib["steel"] != 10 {
		t.Errorf("contrib = %+v", c.Contrib)
	}
}

func TestHandleContribution_ResolvesViaLookupIfUncached(t *testing.T) {
	sess := setupSession() // no cache yet
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-resolved"},
		},
	}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationContribution, map[string]any{
		"MarketID":      3789012345,
		"Contributions": []map[string]any{{"Name": "$titanium_name;", "Amount": 4}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.contributions) != 1 || api.contributions[0].BuildID != "build-resolved" {
		t.Errorf("contributions = %+v", api.contributions)
	}
	if got, _ := sess.BuildFor(3789012345); got != "build-resolved" {
		t.Errorf("cache not populated, got %q", got)
	}
}

func TestHandleContribution_SkipsWhenCommanderUnknown(t *testing.T) {
	sess := state.New() // empty
	sess.SetSystem("Sol", 1)
	sess.SetDocked("foo", 2, 1)
	sess.RememberBuild(2, "b")
	api := &fakeAPI{}
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationContribution, map[string]any{
		"MarketID":      2,
		"Contributions": []map[string]any{{"Name": "$titanium_name;", "Amount": 1}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.contributions) != 0 {
		t.Errorf("should have skipped without commander; got %v", api.contributions)
	}
}

func TestHandleCommander_UpdatesSession(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventCommander, map[string]any{"Name": "Jameson", "FID": "F1"})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if got := sess.Commander(); got != "Jameson" {
		t.Errorf("commander = %q", got)
	}
}

func TestHandleLocation_UpdatesSystem(t *testing.T) {
	sess := state.New()
	r := New(&fakeAPI{}, sess)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":    "Alpha Centauri",
		"SystemAddress": 1234567,
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	name, addr := sess.System()
	if name != "Alpha Centauri" || addr != 1234567 {
		t.Errorf("system = (%q, %d)", name, addr)
	}
}

func TestStatusCallback_FiresForUpdates(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{
		lookupResp: map[lookupKey]*ravencolonial.Project{
			{10477373803, 3789012345}: {BuildID: "build-1"},
		},
	}
	r := New(api, sess)
	var statuses []Status
	r.OnStatus(func(s Status) { statuses = append(statuses, s) })
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":          3789012345,
		"ResourcesRequired": []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 10, "ProvidedAmount": 0}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("expected at least one status update")
	}
	// Final one should be the OK report for the depot.
	last := statuses[len(statuses)-1]
	if last.Level != LevelOK {
		t.Errorf("last status level = %v, want OK", last.Level)
	}
}
