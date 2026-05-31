// Package edsy builds edsy.org "import" URLs from a journal Loadout event.
//
// It mirrors the scheme ED Market Connector uses so the resulting links are
// understood by EDSY's importer: gzip-compress the Loadout JSON, urlsafe
// base64-encode it, percent-escape the base64 padding, and append to the
// EDSY import fragment. EDSY's `#/I=` route reads the journal Loadout event
// verbatim, so we hand it the raw event payload unchanged.
//
// Independent implementation; EDMC (GPLv2) was consulted for the URL shape
// but no code was copied.
package edsy

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"strings"
)

// ImportPrefix is the EDSY import-URL fragment prefix.
const ImportPrefix = "https://edsy.org/#/I="

// URL builds an EDSY import URL from a raw journal Loadout event payload.
// Returns an error if the loadout is empty.
func URL(loadout []byte) (string, error) {
	if len(loadout) == 0 {
		return "", errors.New("edsy: empty loadout")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(loadout); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	// urlsafe base64 (with padding), then percent-escape the '=' padding —
	// exactly the encoding EDSY's importer expects.
	enc := base64.URLEncoding.EncodeToString(buf.Bytes())
	enc = strings.ReplaceAll(enc, "=", "%3D")
	return ImportPrefix + enc, nil
}
