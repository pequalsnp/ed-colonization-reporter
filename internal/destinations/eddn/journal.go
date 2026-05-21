package eddn

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// schemaJournalV1 is the production schemaRef for journal/1 messages.
const schemaJournalV1 = "https://eddn.edcd.io/schemas/journal/1"

// eddnJournalEvents is the closed set of events EDDN accepts on journal/1.
// We only emit the subset we have native source data for; the rest are
// silently ignored.
var eddnJournalEvents = map[string]bool{
	journal.EventFSDJump:     true,
	journal.EventLocation:    true,
	journal.EventDocked:      true,
	journal.EventCarrierJump: true,
}

// errMissingStarPos signals that we cannot satisfy EDDN's StarPos requirement
// for an event that needs it augmented from session state — typically Docked
// when the player's location hasn't been seen yet (e.g. game started while
// already docked).
var errMissingStarPos = errors.New("eddn: StarPos unknown; skipping until next FSDJump/Location")

// buildJournalMessage transforms a parsed journal event into the EDDN
// journal/1 message body (post-strip). Returns the message map or an error
// if the event cannot be uploaded right now.
//
// The session is consulted for cross-checking augmented StarPos on events
// like Docked, and to attach horizons/odyssey flags.
func buildJournalMessage(raw journal.Raw, sess *state.Session) (map[string]any, error) {
	if !eddnJournalEvents[raw.Event] {
		return nil, nil // not our concern; caller should skip
	}
	var msg map[string]any
	if err := json.Unmarshal(raw.Payload, &msg); err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}

	stripJournalForbidden(msg)
	stripLocalised(msg)

	// For Docked: StarSystem/SystemAddress are in the event but StarPos is
	// not. Augment from session, with the EDDN-mandated cross-check that
	// our cached SystemAddress matches the event's.
	if raw.Event == journal.EventDocked {
		eventSysAddr, _ := msg["SystemAddress"].(float64)
		cachedSys, cachedAddr := sess.System()
		cachedPos, hasPos := sess.StarPos()
		if !hasPos || int64(eventSysAddr) != cachedAddr {
			return nil, errMissingStarPos
		}
		if _, ok := msg["StarSystem"]; !ok {
			msg["StarSystem"] = cachedSys
		}
		msg["StarPos"] = []any{cachedPos[0], cachedPos[1], cachedPos[2]}
	}

	// Required fields must all be present after the transformation.
	for _, k := range []string{"timestamp", "event", "StarSystem", "StarPos", "SystemAddress"} {
		if _, ok := msg[k]; !ok {
			return nil, fmt.Errorf("journal/1 missing required field %q", k)
		}
	}

	// Augment horizons/odyssey on the message body. Per EDDN: only include
	// when LoadGame told us; never serialise an unknown as false.
	if h, _ := sess.DLCFlags(); h != nil {
		msg["horizons"] = *h
	}
	if _, o := sess.DLCFlags(); o != nil {
		msg["odyssey"] = *o
	}
	return msg, nil
}
