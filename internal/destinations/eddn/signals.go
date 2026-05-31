package eddn

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// signalBuffer accumulates FSSSignalDiscovered signals for one system so we
// can post them as a single fsssignaldiscovered/1 message (the schema takes
// a signals array). The game emits one journal line per signal during a
// honk; batching mirrors EDMC and is far kinder to EDDN than one POST each.
type signalBuffer struct {
	systemAddr int64
	starSystem string
	starPos    [3]float64
	hasPos     bool
	signals    []map[string]any
}

// ussTypeMissionTarget is the one USSType value the fsssignaldiscovered/1
// schema forbids (it leaks the player's personal mission state).
const ussTypeMissionTarget = "$USS_Type_MissionTarget;"

// fssSignalFields are the per-signal keys the schema permits. TimeRemaining
// (transient) and any *_Localised key are deliberately excluded.
var fssSignalFields = []string{
	"timestamp", "SignalName", "SignalType", "IsStation",
	"USSType", "SpawningState", "SpawningFaction",
	"SpawningPower", "OpposingPower", "ThreatLevel",
}

// bufferFSSSignal decodes one FSSSignalDiscovered event, drops it if it's a
// personal mission-target USS, and appends it to the per-system buffer. When
// a signal for a different system arrives, the existing buffer is flushed
// first.
func (u *Uploader) bufferFSSSignal(ctx context.Context, payload []byte) error {
	ev, err := decodeEvent(payload)
	if err != nil {
		return err
	}
	if ut, _ := ev["USSType"].(string); ut == ussTypeMissionTarget {
		return nil // forbidden by schema; skip
	}
	addr, hasAddr := sysAddrOf(ev)
	if !hasAddr {
		return nil
	}
	sys, cachedAddr := u.Session.System()
	pos, hasPos := u.Session.StarPos()
	if sys == "" || !hasPos || cachedAddr != addr {
		// We can't satisfy StarSystem/StarPos for this signal's system.
		return nil
	}

	sig := pick(ev, fssSignalFields...)

	u.sigMu.Lock()
	if len(u.sigBuf.signals) > 0 && u.sigBuf.systemAddr != addr {
		flush := u.takeSignalsLocked()
		u.sigMu.Unlock()
		_ = u.sendSignals(ctx, flush)
		u.sigMu.Lock()
	}
	u.sigBuf.systemAddr = addr
	u.sigBuf.starSystem = sys
	u.sigBuf.starPos = pos
	u.sigBuf.hasPos = true
	u.sigBuf.signals = append(u.sigBuf.signals, sig)
	u.sigMu.Unlock()
	return nil
}

// takeSignalsLocked detaches the current buffer for sending. Caller holds
// sigMu.
func (u *Uploader) takeSignalsLocked() signalBuffer {
	b := u.sigBuf
	u.sigBuf = signalBuffer{}
	return b
}

// flushSignals posts any buffered signals. Called when the player changes
// system (FSDJump/CarrierJump/Location) or finishes a discovery scan.
func (u *Uploader) flushSignals(ctx context.Context) {
	u.sigMu.Lock()
	if len(u.sigBuf.signals) == 0 {
		u.sigMu.Unlock()
		return
	}
	b := u.takeSignalsLocked()
	u.sigMu.Unlock()
	_ = u.sendSignals(ctx, b)
}

// sendSignals builds and posts the fsssignaldiscovered/1 message for a
// buffer. The message timestamp duplicates the first signal's, per schema.
func (u *Uploader) sendSignals(ctx context.Context, b signalBuffer) error {
	if len(b.signals) == 0 || !b.hasPos {
		return nil
	}
	ts, _ := b.signals[0]["timestamp"].(string)
	msg := map[string]any{
		"event":         "FSSSignalDiscovered",
		"timestamp":     ts,
		"StarSystem":    b.starSystem,
		"StarPos":       []any{b.starPos[0], b.starPos[1], b.starPos[2]},
		"SystemAddress": json.Number(strconv.FormatInt(b.systemAddr, 10)),
		"signals":       b.signals,
	}
	addDLCFlags(msg, u.Session)
	label := fmt.Sprintf("%d signal(s) in %s", len(b.signals), b.starSystem)
	return u.send(ctx, schemaFSSSignalDiscoveredV1, msg, label)
}

// buildFSSBodySignalsMessage builds the fssbodysignals/1 body from either an
// FSSBodySignals event or a SAASignalsFound event (both carry a Signals
// array of {Type, Count}). The schema's event enum is "FSSBodySignals", so
// we force that name even for SAASignalsFound.
func buildFSSBodySignalsMessage(payload []byte, sess *state.Session) (map[string]any, error) {
	ev, err := decodeEvent(payload)
	if err != nil {
		return nil, err
	}
	rawSignals, ok := ev["Signals"].([]any)
	if !ok || len(rawSignals) == 0 {
		return nil, nil // nothing to report
	}
	signals := make([]map[string]any, 0, len(rawSignals))
	for _, s := range rawSignals {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		item := pick(sm, "Type", "Count")
		if _, ok := item["Type"]; !ok {
			continue
		}
		signals = append(signals, item)
	}
	if len(signals) == 0 {
		return nil, nil
	}
	msg := pick(ev, "timestamp", "StarSystem", "SystemAddress", "BodyID", "BodyName")
	msg["event"] = "FSSBodySignals"
	msg["Signals"] = signals
	if err := augmentSystem(msg, "StarSystem", sess); err != nil {
		return nil, err
	}
	if err := requireKeys(schemaFSSBodySignalsV1, msg,
		"timestamp", "event", "StarSystem", "StarPos", "SystemAddress", "BodyID", "Signals"); err != nil {
		return nil, err
	}
	addDLCFlags(msg, sess)
	return msg, nil
}
