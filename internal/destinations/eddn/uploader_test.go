package eddn

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func mustRaw(t *testing.T, event string, payload map[string]any) journal.Raw {
	t.Helper()
	payload["event"] = event
	if _, ok := payload["timestamp"]; !ok {
		payload["timestamp"] = "2026-05-21T12:00:00Z"
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := journal.ParseLine(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// uploaderWithMock returns an Uploader pointed at an httptest server. Body
// of each POST is captured on `captured`.
func uploaderWithMock(t *testing.T, sess *state.Session, captured *[]map[string]any) (*Uploader, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// EDDN expects gzipped uploads; assert the encoding header and
		// decompress before validating the JSON.
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("missing Content-Encoding: gzip; got %q", r.Header.Get("Content-Encoding"))
			http.Error(w, "expected gzip", 400)
			return
		}
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip reader: %v", err)
			http.Error(w, "bad gzip", 400)
			return
		}
		defer gz.Close()
		body, _ := io.ReadAll(gz)
		var env map[string]any
		if err := json.Unmarshal(body, &env); err != nil {
			t.Errorf("server got invalid JSON: %v; body=%s", err, body)
			http.Error(w, "bad json", 400)
			return
		}
		*captured = append(*captured, env)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	u := New(SoftwareID{Name: "edcolreport-test", Version: "0.0.1"}, sess)
	u.Endpoint = srv.URL
	u.SetEnabled(true)
	return u, srv
}

func TestUploader_DisabledReturnsErrDisabled(t *testing.T) {
	sess := state.New()
	u := New(SoftwareID{Name: "test"}, sess)
	// enabled left false
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0, 0, 0},
	})
	if err := u.HandleEvent(context.Background(), raw); !errors.Is(err, destinations.ErrDisabled) {
		t.Errorf("got %v, want ErrDisabled", err)
	}
}

func TestUploader_NoCommanderSkipsSilently(t *testing.T) {
	sess := state.New() // commander unset
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0, 0, 0},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("expected no upload without commander; got %d", len(captured))
	}
}

func TestUploader_FSDJumpUploadsEnvelope(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.1", "r12345/r0 ")
	hh := true
	oo := false
	sess.SetDLCFlags(&hh, &oo)

	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)

	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":     "Sol",
		"StarPos":        []any{0.0, 0.0, 0.0},
		"SystemAddress":  10477373803,
		"FuelLevel":      32.0, // forbidden — must be stripped
		"FuelUsed":       8.0,  // forbidden
		"JumpDist":       14.3, // forbidden
		"Name_Localised": "Sol", // forbidden by pattern
	})

	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d uploads, want 1", len(captured))
	}
	env := captured[0]
	if env["$schemaRef"] != schemaJournalV1 {
		t.Errorf("schemaRef = %v", env["$schemaRef"])
	}
	hdr := env["header"].(map[string]any)
	if hdr["uploaderID"] != "Jameson" {
		t.Errorf("uploaderID = %v", hdr["uploaderID"])
	}
	if hdr["softwareName"] != "edcolreport-test" || hdr["softwareVersion"] != "0.0.1" {
		t.Errorf("software identity wrong: %+v", hdr)
	}
	if hdr["gameversion"] != "4.1" || hdr["gamebuild"] != "r12345/r0 " {
		t.Errorf("game version/build wrong: %+v", hdr)
	}
	msg := env["message"].(map[string]any)
	for _, forbidden := range []string{"FuelLevel", "FuelUsed", "JumpDist", "Name_Localised"} {
		if _, ok := msg[forbidden]; ok {
			t.Errorf("forbidden field %q not stripped from message", forbidden)
		}
	}
	if msg["horizons"] != true || msg["odyssey"] != false {
		t.Errorf("DLC flags wrong: horizons=%v odyssey=%v", msg["horizons"], msg["odyssey"])
	}
	if msg["StarSystem"] != "Sol" {
		t.Errorf("StarSystem = %v", msg["StarSystem"])
	}
}

func TestUploader_DockedAugmentsStarPosFromCache(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.1", "r12345/r0 ")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})

	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)

	raw := mustRaw(t, journal.EventDocked, map[string]any{
		"StationName":   "Abraham Lincoln",
		"StationType":   "Orbis",
		"MarketID":      128666761,
		"SystemAddress": 10477373803,
		"StarSystem":    "Sol",
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d, want 1", len(captured))
	}
	msg := captured[0]["message"].(map[string]any)
	pos, ok := msg["StarPos"].([]any)
	if !ok || len(pos) != 3 {
		t.Errorf("StarPos missing/wrong shape: %v", msg["StarPos"])
	}
}

func TestUploader_DockedDroppedIfSystemMismatch(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})

	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)

	raw := mustRaw(t, journal.EventDocked, map[string]any{
		"StationName":   "Some Station",
		"MarketID":      999,
		"SystemAddress": 99999, // cached doesn't match — must drop
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("docked with mismatched system should not upload; got %d", len(captured))
	}
}

func TestUploader_DockedDroppedIfNoCachedPos(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	// No SetSystemWithPos — we never saw a FSDJump/Location yet.

	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	raw := mustRaw(t, journal.EventDocked, map[string]any{
		"StationName": "X", "MarketID": 1, "SystemAddress": 999,
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("docked without cached pos must not upload; got %d", len(captured))
	}
}

func TestUploader_OmitsDLCFlagsWhenUnknown(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	// no SetDLCFlags

	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0.0, 0.0, 0.0},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	msg := captured[0]["message"].(map[string]any)
	if _, ok := msg["horizons"]; ok {
		t.Error("horizons must be omitted when unknown (not sent as false)")
	}
	if _, ok := msg["odyssey"]; ok {
		t.Error("odyssey must be omitted when unknown")
	}
}

func TestUploader_MarketBuildsCommodityMessage(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")

	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	u.JournalDir = "/fake"
	u.ReadMarket = func(dir string) (*journal.MarketFile, error) {
		return &journal.MarketFile{
			MarketID:    42,
			StationName: "Sol Trading",
			StationType: "Orbis",
			StarSystem:  "Sol",
			Timestamp:   "2026-05-21T13:00:00Z",
			Items: []journal.MarketItem{
				{Name: "$titanium_name;", MeanPrice: 1280, BuyPrice: 1200, Stock: 1000, StockBracket: 3, SellPrice: 1280, Demand: 0, DemandBracket: 0, Category: "$MARKET_category_metals;"},
				{Name: "$drones_name;", MeanPrice: 100, BuyPrice: 100, Stock: 50, Category: "$MARKET_category_NonMarketable;"}, // skipped
			},
		}, nil
	}

	raw := mustRaw(t, journal.EventMarket, map[string]any{
		"MarketID": 42, "StationType": "Orbis", "StarSystem": "Sol",
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d, want 1", len(captured))
	}
	env := captured[0]
	if env["$schemaRef"] != schemaCommodityV3 {
		t.Errorf("schemaRef = %v", env["$schemaRef"])
	}
	msg := env["message"].(map[string]any)
	if msg["systemName"] != "Sol" || msg["stationName"] != "Sol Trading" {
		t.Errorf("renames wrong: %+v", msg)
	}
	if msg["marketId"].(float64) != 42 {
		t.Errorf("marketId = %v", msg["marketId"])
	}
	commodities := msg["commodities"].([]any)
	if len(commodities) != 1 {
		t.Fatalf("commodities = %d, want 1 (limpets filtered)", len(commodities))
	}
	first := commodities[0].(map[string]any)
	if first["name"] != "titanium" {
		t.Errorf("name not unwrapped: %v", first["name"])
	}
	if _, ok := first["id"]; ok {
		t.Error("id must not be present")
	}
}

func TestUploader_MarketSkipsStaleMarketFile(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	u.JournalDir = "/fake"
	u.ReadMarket = func(dir string) (*journal.MarketFile, error) {
		return &journal.MarketFile{MarketID: 999, Items: nil}, nil // wrong market
	}
	raw := mustRaw(t, journal.EventMarket, map[string]any{"MarketID": 42})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("stale Market.json should not upload")
	}
}

func TestUploader_ServerErrorReturnsError(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "validation failed: missing thing", 400)
	}))
	defer srv.Close()
	u := New(SoftwareID{Name: "t", Version: "x"}, sess)
	u.Endpoint = srv.URL
	u.SetEnabled(true)

	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0.0, 0.0, 0.0},
	})
	err := u.HandleEvent(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error from EDDN 400")
	}
}

func TestUploader_SkipsBetaGameversion(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.0.0.beta3", "r999/r0 ")
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0.0, 0.0, 0.0},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("beta gameversion must not upload; EDDN rejects them at the gateway. got %d", len(captured))
	}
}

func TestUploader_SkipsLegacyGameversion(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	sess.SetGameVersion("4.0.0.1 Legacy", "r999/r0 ")
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0.0, 0.0, 0.0},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("legacy gameversion belongs on a separate relay; got %d uploads", len(captured))
	}
}

func TestUploader_OmitsEmptyGameversionFromHeader(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	// No SetGameVersion call — header should omit gameversion/gamebuild
	// rather than send empty strings.
	captured := []map[string]any{}
	u, _ := uploaderWithMock(t, sess, &captured)
	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "SystemAddress": 1, "StarPos": []any{0.0, 0.0, 0.0},
	})
	if err := u.HandleEvent(context.Background(), raw); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d, want 1", len(captured))
	}
	hdr := captured[0]["header"].(map[string]any)
	if _, ok := hdr["gameversion"]; ok {
		t.Errorf("gameversion must be omitted when unknown; got %v", hdr["gameversion"])
	}
	if _, ok := hdr["gamebuild"]; ok {
		t.Errorf("gamebuild must be omitted when unknown; got %v", hdr["gamebuild"])
	}
}

func TestStripCommodityWrapper(t *testing.T) {
	cases := map[string]string{
		"$titanium_name;":           "titanium",
		"$Ceramic_Composites_name;": "ceramic_composites",
		"titanium":                  "titanium",
		"TITANIUM":                  "titanium",
		"":                          "",
	}
	for in, want := range cases {
		if got := stripCommodityWrapper(in); got != want {
			t.Errorf("stripCommodityWrapper(%q) = %q, want %q", in, got, want)
		}
	}
}
