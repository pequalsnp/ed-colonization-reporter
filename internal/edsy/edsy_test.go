package edsy

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

func TestURL_EmptyLoadout(t *testing.T) {
	if _, err := URL(nil); err == nil {
		t.Fatal("expected error for empty loadout")
	}
}

func TestURL_RoundTrips(t *testing.T) {
	loadout := []byte(`{"timestamp":"2026-05-30T00:00:00Z","event":"Loadout","Ship":"anaconda","ShipID":7,"Modules":[{"Slot":"PowerPlant","Item":"int_powerplant_size8_class5","On":true}]}`)

	u, err := URL(loadout)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, ImportPrefix) {
		t.Fatalf("URL missing prefix: %s", u)
	}

	// Reverse the encoding the way EDSY's importer does and confirm we get
	// the original loadout bytes back.
	enc := strings.TrimPrefix(u, ImportPrefix)
	enc = strings.ReplaceAll(enc, "%3D", "=")
	gzipped, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(got, loadout) {
		t.Errorf("round-trip mismatch:\n got: %s\nwant: %s", got, loadout)
	}
}

func TestURL_NoBareEqualsInOutput(t *testing.T) {
	// The padding must be percent-escaped, never a bare '=' (which would
	// break the URL fragment).
	u, err := URL([]byte(`{"event":"Loadout","Ship":"sidewinder"}`))
	if err != nil {
		t.Fatal(err)
	}
	frag := strings.TrimPrefix(u, ImportPrefix)
	if strings.Contains(frag, "=") {
		t.Errorf("fragment contains bare '='; padding not escaped: %s", frag)
	}
}
