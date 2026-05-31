package eddn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// Schema refs for the dedicated exploration/scan schemas. Each has its own
// strict (additionalProperties:false) message shape, so we build these with
// an explicit field allowlist rather than passing the journal event through.
const (
	schemaFSSDiscoveryScanV1    = "https://eddn.edcd.io/schemas/fssdiscoveryscan/1"
	schemaFSSAllBodiesFoundV1   = "https://eddn.edcd.io/schemas/fssallbodiesfound/1"
	schemaNavBeaconScanV1       = "https://eddn.edcd.io/schemas/navbeaconscan/1"
	schemaScanBaryCentreV1      = "https://eddn.edcd.io/schemas/scanbarycentre/1"
	schemaApproachSettlementV1  = "https://eddn.edcd.io/schemas/approachsettlement/1"
	schemaCodexEntryV1          = "https://eddn.edcd.io/schemas/codexentry/1"
	schemaFSSBodySignalsV1      = "https://eddn.edcd.io/schemas/fssbodysignals/1"
	schemaFSSSignalDiscoveredV1 = "https://eddn.edcd.io/schemas/fsssignaldiscovered/1"
	schemaNavRouteV1            = "https://eddn.edcd.io/schemas/navroute/1"
)

// decodeEvent decodes a raw journal payload into a map, preserving numeric
// precision via json.Number. SystemAddress (id64) exceeds float64's exact
// integer range, so a plain Unmarshal would corrupt it.
func decodeEvent(payload []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}
	return m, nil
}

// sysAddrOf returns the SystemAddress of a decoded event as int64.
func sysAddrOf(m map[string]any) (int64, bool) {
	n, ok := m["SystemAddress"].(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Int64()
	if err != nil {
		return 0, false
	}
	return v, true
}

// augmentSystem ensures the message carries the system-name key (nameKey is
// "StarSystem", "System", or "SystemName"), StarPos, and SystemAddress —
// pulling from session state for events that don't carry them inline (Scan,
// FSSDiscoveryScan, Docked, …). Events that are already self-contained
// (FSDJump/Location/CarrierJump) are left untouched.
//
// EDDN mandates a cross-check: only attach session coordinates when the
// event's SystemAddress matches the system we have cached coords for, so a
// stale session never mislabels a scan. Returns errMissingStarPos when we
// can't satisfy the requirement safely (caller skips quietly).
func augmentSystem(msg map[string]any, nameKey string, sess *state.Session) error {
	_, hasName := msg[nameKey]
	_, hasPos := msg["StarPos"]
	if hasName && hasPos {
		return nil // self-contained event
	}
	cachedSys, cachedAddr := sess.System()
	cachedPos, hasCachedPos := sess.StarPos()
	if cachedSys == "" || !hasCachedPos {
		return errMissingStarPos
	}
	if evAddr, ok := sysAddrOf(msg); ok && evAddr != cachedAddr {
		// Event belongs to a different system than our cached coords.
		return errMissingStarPos
	}
	if !hasName {
		msg[nameKey] = cachedSys
	}
	if !hasPos {
		msg["StarPos"] = []any{cachedPos[0], cachedPos[1], cachedPos[2]}
	}
	if _, ok := msg["SystemAddress"]; !ok {
		msg["SystemAddress"] = json.Number(strconv.FormatInt(cachedAddr, 10))
	}
	return nil
}

// pick copies the named keys from src into a fresh map, skipping absent keys.
// This is the allowlist that keeps additionalProperties:false schemas happy.
func pick(src map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	return out
}

// requireKeys returns an error naming the first required key missing from msg.
func requireKeys(schema string, msg map[string]any, keys ...string) error {
	for _, k := range keys {
		if _, ok := msg[k]; !ok {
			return fmt.Errorf("%s missing required field %q", shortSchema(schema), k)
		}
	}
	return nil
}

// buildScanMessage builds a "system + scalar fields" dedicated-schema body
// (fssdiscoveryscan, fssallbodiesfound, navbeaconscan, scanbarycentre). It
// copies event/timestamp + the system fields (augmented) + the named scalar
// fields, then validates the required set. requiredScalars must all be
// present; optionalScalars are copied when present.
func buildScanMessage(payload []byte, schema, nameKey string, requiredScalars, optionalScalars []string, sess *state.Session) (map[string]any, error) {
	ev, err := decodeEvent(payload)
	if err != nil {
		return nil, err
	}
	msg := pick(ev, "timestamp", "event", nameKey, "SystemAddress")
	for _, k := range append(append([]string{}, requiredScalars...), optionalScalars...) {
		if v, ok := ev[k]; ok {
			msg[k] = v
		}
	}
	if err := augmentSystem(msg, nameKey, sess); err != nil {
		return nil, err
	}
	required := append([]string{"timestamp", "event", nameKey, "StarPos", "SystemAddress"}, requiredScalars...)
	if err := requireKeys(schema, msg, required...); err != nil {
		return nil, err
	}
	addDLCFlags(msg, sess)
	return msg, nil
}

// buildApproachSettlementMessage builds the approachsettlement/1 body. The
// schema requires Latitude/Longitude, which the journal omits when the
// settlement is approached from orbit — those events are skipped (nil,nil).
func buildApproachSettlementMessage(payload []byte, sess *state.Session) (map[string]any, error) {
	ev, err := decodeEvent(payload)
	if err != nil {
		return nil, err
	}
	if _, ok := ev["Latitude"]; !ok {
		return nil, nil // no surface position; schema can't be satisfied
	}
	if _, ok := ev["Longitude"]; !ok {
		return nil, nil
	}
	msg := pick(ev,
		"timestamp", "event", "StarSystem", "SystemAddress",
		"Name", "BodyID", "BodyName", "Latitude", "Longitude",
		"MarketID", "StationGovernment", "StationAllegiance",
		"StationEconomy", "StationEconomies", "StationFaction", "StationServices",
	)
	stripLocalised(msg)
	if err := augmentSystem(msg, "StarSystem", sess); err != nil {
		return nil, err
	}
	if err := requireKeys(schemaApproachSettlementV1, msg,
		"timestamp", "event", "StarSystem", "StarPos", "SystemAddress",
		"Name", "BodyID", "BodyName", "Latitude", "Longitude"); err != nil {
		return nil, err
	}
	addDLCFlags(msg, sess)
	return msg, nil
}

// buildCodexEntryMessage builds the codexentry/1 body. Note the schema keys
// the system on "System" (not StarSystem), and forbids the personal-progress
// fields IsNewEntry / NewTraitsDiscovered.
func buildCodexEntryMessage(payload []byte, sess *state.Session) (map[string]any, error) {
	ev, err := decodeEvent(payload)
	if err != nil {
		return nil, err
	}
	msg := pick(ev,
		"timestamp", "event", "System", "SystemAddress", "EntryID",
		"Name", "Region", "Category", "SubCategory",
		"Latitude", "Longitude", "BodyID", "BodyName",
		"NearestDestination", "VoucherAmount", "Traits",
	)
	stripLocalised(msg)
	if err := augmentSystem(msg, "System", sess); err != nil {
		return nil, err
	}
	if err := requireKeys(schemaCodexEntryV1, msg,
		"timestamp", "event", "System", "StarPos", "SystemAddress", "EntryID"); err != nil {
		return nil, err
	}
	addDLCFlags(msg, sess)
	return msg, nil
}
