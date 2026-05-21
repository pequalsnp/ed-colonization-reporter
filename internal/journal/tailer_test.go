package journal

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	cmdrLine = `{"timestamp":"2026-05-21T12:30:45Z","event":"Commander","FID":"F1","Name":"X"}` + "\n"
	loadLine = `{"timestamp":"2026-05-21T12:30:46Z","event":"LoadGame","Commander":"X","FID":"F1"}` + "\n"
)

func TestTailer_ReadsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "Journal.001.log")
	if err := os.WriteFile(jpath, []byte(cmdrLine), 0o644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtBeginning,
	}
	events := collectEvents(t, tl, 2, 2*time.Second, func() {
		time.Sleep(50 * time.Millisecond) // let the tailer reach EOF
		appendFile(t, jpath, loadLine)
	})

	if got := eventNames(events); !equalStrings(got, []string{EventCommander, EventLoadGame}) {
		t.Errorf("got events %v, want [Commander LoadGame]", got)
	}
}

func TestTailer_HandlesRotation(t *testing.T) {
	dir := t.TempDir()
	jpath1 := filepath.Join(dir, "Journal.001.log")
	if err := os.WriteFile(jpath1, []byte(cmdrLine), 0o644); err != nil {
		t.Fatalf("write j1: %v", err)
	}

	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtBeginning,
	}
	events := collectEvents(t, tl, 2, 2*time.Second, func() {
		// Wait for the tailer to consume Journal.001.log, then create a
		// newer file. Bump mtime explicitly because the test creates files
		// faster than typical filesystem resolution.
		time.Sleep(80 * time.Millisecond)
		jpath2 := filepath.Join(dir, "Journal.002.log")
		if err := os.WriteFile(jpath2, []byte(loadLine), 0o644); err != nil {
			t.Fatalf("write j2: %v", err)
		}
		future := time.Now().Add(1 * time.Hour)
		_ = os.Chtimes(jpath2, future, future)
	})

	names := eventNames(events)
	if !equalStrings(names, []string{EventCommander, EventLoadGame}) {
		t.Errorf("got events %v, want [Commander LoadGame] across rotation", names)
	}
}

func TestTailer_StartAtEndSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "Journal.001.log")
	// Pre-existing line that should NOT be emitted.
	if err := os.WriteFile(jpath, []byte(cmdrLine), 0o644); err != nil {
		t.Fatalf("write initial: %v", err)
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

	if got := eventNames(events); !equalStrings(got, []string{EventLoadGame}) {
		t.Errorf("got %v, want [LoadGame] only (Commander pre-existed)", got)
	}
}

func TestTailer_IgnoresMalformedLines(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "Journal.001.log")
	content := "not json at all\n" + cmdrLine + `{"missing":"event"}` + "\n" + loadLine
	if err := os.WriteFile(jpath, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtBeginning,
	}
	events := collectEvents(t, tl, 2, 1*time.Second, func() {})
	if got := eventNames(events); !equalStrings(got, []string{EventCommander, EventLoadGame}) {
		t.Errorf("got %v, want [Commander LoadGame] — malformed lines must be skipped", got)
	}
}

func TestTailer_PartialLineWaitsForNewline(t *testing.T) {
	dir := t.TempDir()
	jpath := filepath.Join(dir, "Journal.001.log")
	// Write half a JSON object first; no event should be emitted yet.
	partial := `{"timestamp":"2026-05-21T12:30:45Z","event":"Comm`
	if err := os.WriteFile(jpath, []byte(partial), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	tl := &Tailer{
		Dir:          dir,
		PollInterval: 10 * time.Millisecond,
		StartAt:      StartAtBeginning,
	}
	events := collectEvents(t, tl, 1, 1*time.Second, func() {
		time.Sleep(80 * time.Millisecond)
		appendFile(t, jpath, `ander","FID":"F1","Name":"X"}`+"\n")
	})
	if got := eventNames(events); !equalStrings(got, []string{EventCommander}) {
		t.Errorf("got %v, want [Commander] once line completed", got)
	}
}

// collectEvents runs the tailer until n events are collected or timeout, then
// cancels. `during` is run inline after the tailer is started, so callers
// can mutate the journal mid-run.
func collectEvents(t *testing.T, tl *Tailer, n int, timeout time.Duration, during func()) []Raw {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ch := make(chan Raw, 32)
	var (
		wg      sync.WaitGroup
		runErr  error
		events  []Raw
		eventsM sync.Mutex
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = tl.Run(ctx, ch)
	}()

	collected := make(chan struct{})
	go func() {
		for r := range ch {
			eventsM.Lock()
			events = append(events, r)
			done := len(events) >= n
			eventsM.Unlock()
			if done {
				cancel()
			}
		}
		close(collected)
	}()

	if during != nil {
		during()
	}

	<-collected
	wg.Wait()
	if runErr != nil && runErr != context.Canceled && runErr != context.DeadlineExceeded {
		t.Fatalf("tailer error: %v", runErr)
	}
	return events
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func eventNames(rs []Raw) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Event
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
