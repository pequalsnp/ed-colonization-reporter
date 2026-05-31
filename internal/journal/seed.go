package journal

import (
	"bufio"
	"fmt"
	"os"
)

// stateSeedEvents are the journal events that establish durable *current
// state* — commander identity, game version, ship loadout, and inventories —
// rather than point-in-time activity (jumps, docks, scans). The game writes
// them in the preamble at the top of every journal file. When the tailer
// starts at end-of-file (replay disabled) the preamble is never read, so
// materials/ship/cargo state stays empty until the next live occurrence of
// each — which for a snapshot like Materials only happens on a game restart.
var stateSeedEvents = map[string]bool{
	EventFileheader: true, // game version
	EventCommander:  true, // commander name/FID (also kicks off FC cargo seed)
	EventLoadGame:   true, // commander, game version, DLC flags
	EventLoadout:    true, // current ship (EDSY link, Inara ship)
	EventMaterials:  true, // engineering-materials inventory snapshot
	EventCargo:      true, // ship cargo snapshot
}

// seedScanBuffer is large enough for a fully-engineered Loadout line, which
// is the biggest event we seed.
const seedScanBuffer = 1 << 20 // 1 MiB

// ReadStateSeed scans the most recent journal file and returns its
// state-snapshot events (see stateSeedEvents) in file order, each flagged
// Replayed=true.
//
// Emitting these through the destination multiplex seeds session and
// inventory state without re-uploading travel or activity: every destination
// skips Replayed events except the stateful seeders — the reporter (ship,
// cargo, commander) and the Inara uploader (the materials tally). This makes
// materials/ship/cargo state available at launch even when session replay is
// disabled, which otherwise skips the journal preamble entirely.
func ReadStateSeed(dir string) ([]Raw, error) {
	path, err := LatestJournalFile(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var out []Raw
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), seedScanBuffer)
	for sc.Scan() {
		raw, perr := ParseLine(sc.Bytes())
		if perr != nil {
			continue
		}
		if !stateSeedEvents[raw.Event] {
			continue
		}
		raw.Replayed = true
		out = append(out, raw)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}
