package reporter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// fakeAPI is a minimal stub of the ravencolonial APIClient used to assert
// what the reporter calls.
type fakeAPI struct {
	lookups       []lookupCall
	updates       []ravencolonial.ProjectUpdate
	completes     []string
	contributions []contributionCall
	lookupResp    map[lookupKey]*ravencolonial.Project
	lookupErr     map[lookupKey]error
	updateErr     error
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

func (f *fakeAPI) CompleteProject(_ context.Context, buildID string) error {
	f.completes = append(f.completes, buildID)
	return nil
}

func (f *fakeAPI) Contribute(_ context.Context, buildID, cmdr string, c ravencolonial.Contribution) error {
	f.contributions = append(f.contributions, contributionCall{buildID, cmdr, c})
	return nil
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
	if u.MaxNeed != 600 {
		t.Errorf("maxNeed = %d, want 600", u.MaxNeed)
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

func TestHandleDepot_NotFoundSkipsQuietly(t *testing.T) {
	sess := setupSession()
	api := &fakeAPI{} // empty: all lookups 404
	r := New(api, sess)
	raw := mustRaw(t, journal.EventColonisationConstructionDepot, map[string]any{
		"MarketID":             3789012345,
		"ResourcesRequired":    []map[string]any{{"Name": "$titanium_name;", "RequiredAmount": 100, "ProvidedAmount": 0}},
	})
	if err := r.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(api.updates) != 0 {
		t.Errorf("should not have updated for 404 lookup")
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
