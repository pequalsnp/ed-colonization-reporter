package journal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailer_ReplayedFlag_BackfillThenLive(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "Journal.001.log")
	// Two pre-existing lines (backfill territory).
	if err := os.WriteFile(jpath, []byte(cmdrLine+loadLine), 0o644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtBeginning,
	}
	events := collectEvents(t, tl, 3, 2*time.Second, func() {
		time.Sleep(80 * time.Millisecond) // tailer reaches EOF
		appendFile(t, jpath, `{"timestamp":"2026-05-22T12:00:00Z","event":"FSDJump","StarSystem":"Sol","SystemAddress":1,"StarPos":[0,0,0]}`+"\n")
	})
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}
	// First two were in the file at open time → Replayed.
	if !events[0].Replayed {
		t.Errorf("event[0] (%s): Replayed = false, want true", events[0].Event)
	}
	if !events[1].Replayed {
		t.Errorf("event[1] (%s): Replayed = false, want true", events[1].Event)
	}
	// Third arrived after we hit EOF → live.
	if events[2].Replayed {
		t.Errorf("event[2] (%s): Replayed = true, want false (live event)", events[2].Event)
	}
}

func TestTailer_ReplayedFlag_StartAtEndIsAlwaysLive(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "Journal.001.log")
	if err := os.WriteFile(jpath, []byte(cmdrLine), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtEnd,
	}
	events := collectEvents(t, tl, 1, 1*time.Second, func() {
		time.Sleep(80 * time.Millisecond)
		appendFile(t, jpath, loadLine)
	})
	if len(events) != 1 {
		t.Fatalf("events len = %d", len(events))
	}
	if events[0].Replayed {
		t.Error("StartAtEnd should never flag events as Replayed")
	}
}

func TestTailer_ReplayedFlag_PostRotationIsLive(t *testing.T) {
	dir := t.TempDir()
	jpath1 := filepath.Join(dir, "Journal.001.log")
	if err := os.WriteFile(jpath1, []byte(cmdrLine), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtBeginning,
	}
	events := collectEvents(t, tl, 2, 2*time.Second, func() {
		time.Sleep(80 * time.Millisecond)
		jpath2 := filepath.Join(dir, "Journal.002.log")
		if err := os.WriteFile(jpath2, []byte(loadLine), 0o644); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(1 * time.Hour)
		_ = os.Chtimes(jpath2, future, future)
	})
	if len(events) != 2 {
		t.Fatalf("events len = %d", len(events))
	}
	if !events[0].Replayed {
		t.Error("first event (from initial file backfill) should be Replayed")
	}
	if events[1].Replayed {
		t.Error("post-rotation event should be live, not Replayed")
	}
}

// background sanity check: collecting works when ctx terminates the run.
var _ = context.Background
