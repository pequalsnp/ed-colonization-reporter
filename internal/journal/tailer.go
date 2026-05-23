package journal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// DefaultPollInterval is how often the tailer checks for journal growth and
// rotation when no interval is configured.
const DefaultPollInterval = 500 * time.Millisecond

// StartPosition controls where the tailer begins reading when it first opens
// a journal file.
type StartPosition int

const (
	// StartAtEnd skips the existing contents of the most recent journal and
	// only emits new lines written after the tailer starts. This is the
	// production default — we don't want to replay an entire 4-hour session
	// of stale events.
	StartAtEnd StartPosition = iota
	// StartAtBeginning replays the current journal file from its first byte.
	// Useful for tests and for bootstrap/backfill flows.
	StartAtBeginning
)

// Tailer watches an Elite Dangerous journal directory and emits parsed events
// as they are appended to the active journal file. It transparently handles
// rotation when the game starts a new journal file.
//
// A Tailer is single-use: call Run once with a cancellable context.
type Tailer struct {
	Dir          string
	PollInterval time.Duration
	StartAt      StartPosition

	// OnLiveMode, if set, is called exactly once when the tailer transitions
	// from backfill replay to live mode. For StartAtEnd that is at the start
	// of Run (no backfill); for StartAtBeginning that is when the initial
	// EOF is reached. Callers use this to gate operations that need a fully
	//-replayed session state — e.g. anchoring a cAPI snapshot to the latest
	// CarrierStats timestamp in the journal.
	OnLiveMode func()

	// Now is injected for tests; callers should leave it nil.
	Now func() time.Time
}

// Run blocks, polling the journal directory and emitting events to out until
// ctx is cancelled. Lines that fail to parse are dropped silently (they are
// usually events we don't model); fatal errors (e.g. the journal directory
// disappears) are returned.
//
// Run closes out before returning.
func (t *Tailer) Run(ctx context.Context, out chan<- Raw) error {
	defer close(out)

	interval := t.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}

	currentPath := ""
	var currentFile *os.File
	defer func() {
		if currentFile != nil {
			currentFile.Close()
		}
	}()

	// partial accumulates bytes read between newlines, in case a journal
	// write happens mid-line and we observe an incomplete trailing fragment.
	var partial []byte

	// replaying is true while we're emitting events from before the file's
	// current EOF on a fresh StartAtBeginning open. It flips to false the
	// first time readMore reaches EOF, so every event read after that is
	// flagged as a "live" event.
	replaying := t.StartAt == StartAtBeginning

	// liveModeFired ensures OnLiveMode is invoked at most once.
	liveModeFired := false
	fireLiveMode := func() {
		if liveModeFired {
			return
		}
		liveModeFired = true
		if t.OnLiveMode != nil {
			t.OnLiveMode()
		}
	}
	if !replaying {
		// StartAtEnd: there is no backfill, we are already live.
		fireLiveMode()
	}

	emit := func(raw Raw) error {
		select {
		case out <- raw:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	openLatest := func() error {
		latest, err := LatestJournalFile(t.Dir)
		if err != nil {
			return err
		}
		if latest == currentPath {
			return nil
		}
		if currentFile != nil {
			currentFile.Close()
			currentFile = nil
		}
		f, err := os.Open(latest)
		if err != nil {
			return fmt.Errorf("open %s: %w", latest, err)
		}
		// If this is the very first file we've opened in this run, honour
		// StartAt. For rotations within a session, always read from the
		// start of the new file so we don't miss the LoadGame/Commander
		// preamble.
		isFirst := currentPath == ""
		currentPath = latest
		currentFile = f
		partial = partial[:0]
		if isFirst && t.StartAt == StartAtEnd {
			if _, err := f.Seek(0, io.SeekEnd); err != nil {
				return fmt.Errorf("seek %s: %w", latest, err)
			}
		}
		return nil
	}

	readMore := func() error {
		if currentFile == nil {
			return nil
		}
		buf := make([]byte, 64*1024)
		for {
			n, err := currentFile.Read(buf)
			if n > 0 {
				partial = append(partial, buf[:n]...)
				for {
					idx := bytes.IndexByte(partial, '\n')
					if idx < 0 {
						break
					}
					line := partial[:idx]
					partial = partial[idx+1:]
					raw, perr := ParseLine(line)
					if perr != nil {
						continue // skip empty/garbage lines
					}
					raw.Replayed = replaying
					if eerr := emit(raw); eerr != nil {
						return eerr
					}
				}
			}
			if err == io.EOF {
				// We've drained the file. Anything that arrives next is
				// genuinely new — flip out of replay mode for future reads
				// and signal callers that the backfill is complete.
				if replaying {
					replaying = false
					fireLiveMode()
				}
				return nil
			}
			if err != nil {
				return fmt.Errorf("read journal: %w", err)
			}
		}
	}

	// Try once up front so the first tick already has a file open. Don't
	// hard-fail if the directory has no journal yet — the game might not
	// have been launched. We'll retry on each tick.
	_ = openLatest()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		// Check for rotation first; if a new file appeared, switch to it.
		if err := openLatest(); err != nil && !errors.Is(err, ErrJournalDirNotFound) {
			// Directory or file vanished — keep polling. A user might be
			// mid-config and not yet have the right path set.
			continue
		}
		if err := readMore(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			// Transient read error — log via a status channel in future, for
			// now just continue. Returning here would kill the tailer.
			continue
		}
	}
}

// IsJournalDirReadable is a small helper for the UI to surface "did you point
// me at the right place?" feedback.
func IsJournalDirReadable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	// Try to actually list the directory — Stat passes even when the dir
	// exists but is unreadable, which is a common Proton-prefix mistake.
	if _, err := filepath.Glob(filepath.Join(dir, "Journal.*.log")); err != nil {
		return fmt.Errorf("glob journals in %s: %w", dir, err)
	}
	return nil
}
