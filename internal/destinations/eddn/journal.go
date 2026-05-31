package eddn

import (
	"errors"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// schemaJournalV1 is the production schemaRef for journal/1 messages.
const schemaJournalV1 = "https://eddn.edcd.io/schemas/journal/1"

// eddnJournalEvents is the closed set of events we emit on the journal/1
// schema. The schema accepts more, but we relay the subset we have native
// source data for. Scan and SAASignalsFound don't carry the player's system
// name/coords inline and are augmented from session state (see augmentSystem).
var eddnJournalEvents = map[string]bool{
	journal.EventFSDJump:         true,
	journal.EventLocation:        true,
	journal.EventDocked:          true,
	journal.EventCarrierJump:     true,
	journal.EventScan:            true,
	journal.EventSAASignalsFound: true,
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
	// Decode preserving numeric precision — SystemAddress (id64) exceeds
	// float64's exact integer range and the augment cross-check compares it.
	msg, err := decodeEvent(raw.Payload)
	if err != nil {
		return nil, err
	}

	stripJournalForbidden(msg)
	stripLocalised(msg)

	// Docked/Scan/SAASignalsFound don't carry StarSystem/StarPos inline;
	// augment from session with the EDDN-mandated SystemAddress cross-check.
	// FSDJump/Location/CarrierJump are self-contained and pass through.
	if err := augmentSystem(msg, "StarSystem", sess); err != nil {
		return nil, err
	}

	// Required fields must all be present after the transformation.
	if err := requireKeys(schemaJournalV1, msg, "timestamp", "event", "StarSystem", "StarPos", "SystemAddress"); err != nil {
		return nil, err
	}

	addDLCFlags(msg, sess)
	return msg, nil
}
