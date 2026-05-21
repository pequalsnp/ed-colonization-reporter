//go:build integration

// To run: `go test -tags integration ./internal/destinations/eddn/...`
//
// These tests POST real EDDN envelopes to the live gateway
// (https://eddn.edcd.io:4430/upload/) with a `/test`-suffixed schemaRef
// so the message is validated against the schema but is not broadcast to
// live consumers — this is EDDN's documented developer-testing path.
// (The dedicated beta gateway exists but is often unavailable, so we
// stick to the canonical /test path.)
//
// These tests confirm our envelope construction passes EDDN's actual
// JSON-schema validator — something pure unit tests can never catch.
//
// The tests skip if the EDDN_INTEGRATION env var is unset so accidental
// runs don't pelt the gateway. CI does not run them by default.

package eddn

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("EDDN_INTEGRATION") == "" {
		t.Skip("set EDDN_INTEGRATION=1 to run; posts to beta.eddn.edcd.io")
	}
}

// realTestModeUploader builds an Uploader against the live EDDN endpoint
// with TestMode on, so schemaRef has `/test` appended and the gateway
// validates without broadcasting.
func realTestModeUploader(t *testing.T, sess *state.Session) *Uploader {
	t.Helper()
	u := New(SoftwareID{Name: "edcolreport-integration-test", Version: "0.0.1"}, sess)
	// Keep the default live Endpoint; TestMode + /test schemaRef is enough.
	u.TestMode = true
	u.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	u.SetEnabled(true)
	return u
}

func TestIntegration_FSDJump_AcceptedByBeta(t *testing.T) {
	requireIntegration(t)

	sess := state.New()
	sess.SetCommander("edcolreport-integration", "F0")
	sess.SetGameVersion("4.0.0.1903", "r12345/r0 ")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})

	u := realTestModeUploader(t, sess)
	var lastLevel, lastMsg string
	u.OnStatus = func(level, msg string) {
		lastLevel, lastMsg = level, msg
		t.Logf("EDDN status: %s — %s", level, msg)
	}

	raw := mustRaw(t, journal.EventFSDJump, map[string]any{
		"StarSystem":     "Sol",
		"StarPos":        []any{0.0, 0.0, 0.0},
		"SystemAddress":  10477373803,
		"FuelLevel":      32.0,
		"FuelUsed":       8.0,
		"JumpDist":       14.3,
		"Population":     22780871769,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := u.HandleEvent(ctx, raw); err != nil {
		t.Fatalf("HandleEvent rejected: %v (lastStatus=%s %s)", err, lastLevel, lastMsg)
	}
	if lastLevel != "OK" {
		t.Errorf("expected OK status from beta endpoint; got %q: %s", lastLevel, lastMsg)
	}
}

func TestIntegration_Docked_AcceptedByBeta(t *testing.T) {
	requireIntegration(t)

	sess := state.New()
	sess.SetCommander("edcolreport-integration", "F0")
	sess.SetGameVersion("4.0.0.1903", "r12345/r0 ")
	sess.SetSystemWithPos("Sol", 10477373803, [3]float64{0, 0, 0})

	u := realTestModeUploader(t, sess)
	var lastLevel string
	u.OnStatus = func(level, msg string) {
		lastLevel = level
		t.Logf("EDDN status: %s — %s", level, msg)
	}

	raw := mustRaw(t, journal.EventDocked, map[string]any{
		"StationName":   "Abraham Lincoln",
		"StationType":   "Orbis",
		"MarketID":      128666761,
		"SystemAddress": 10477373803,
		"StarSystem":    "Sol",
		// Forbidden fields that our stripper must remove for the schema
		// validator to accept the message.
		"Wanted":        true,
		"ActiveFine":    false,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := u.HandleEvent(ctx, raw); err != nil {
		t.Fatalf("HandleEvent rejected: %v", err)
	}
	if lastLevel != "OK" {
		t.Errorf("expected OK status; got %q", lastLevel)
	}
}

func TestIntegration_Commodity_AcceptedByBeta(t *testing.T) {
	requireIntegration(t)

	sess := state.New()
	sess.SetCommander("edcolreport-integration", "F0")
	sess.SetGameVersion("4.0.0.1903", "r12345/r0 ")

	u := realTestModeUploader(t, sess)
	u.JournalDir = "/fake"
	u.ReadMarket = func(dir string) (*journal.MarketFile, error) {
		return &journal.MarketFile{
			MarketID:    128666761,
			StationName: "Abraham Lincoln",
			StationType: "Orbis",
			StarSystem:  "Sol",
			Timestamp:   "2026-05-21T12:00:00Z",
			Items: []journal.MarketItem{
				{
					Name:          "$titanium_name;",
					MeanPrice:     1280,
					BuyPrice:      1200,
					Stock:         500,
					StockBracket:  3,
					SellPrice:     1280,
					Demand:        0,
					DemandBracket: 0,
				},
				{
					Name:          "$gold_name;",
					MeanPrice:     9000,
					BuyPrice:      0,
					Stock:         0,
					StockBracket:  0,
					SellPrice:     9200,
					Demand:        100,
					DemandBracket: 2,
				},
			},
		}, nil
	}
	var lastLevel string
	u.OnStatus = func(level, msg string) {
		lastLevel = level
		t.Logf("EDDN status: %s — %s", level, msg)
	}

	raw := mustRaw(t, journal.EventMarket, map[string]any{
		"MarketID":    128666761,
		"StationType": "Orbis",
		"StarSystem":  "Sol",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := u.HandleEvent(ctx, raw); err != nil {
		t.Fatalf("HandleEvent rejected: %v", err)
	}
	if lastLevel != "OK" {
		t.Errorf("expected OK status; got %q", lastLevel)
	}
}
