package destinations

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
)

// fakeDest is a Destination that records the events it receives. It can be
// configured to fail or return ErrDisabled to exercise multiplex error paths.
type fakeDest struct {
	mu         sync.Mutex
	name       string
	called     int
	events     []string
	fail       error
	disabled   bool
	concurrent atomic.Int32
}

func (f *fakeDest) Name() string { return f.name }

func (f *fakeDest) HandleEvent(_ context.Context, raw journal.Raw) error {
	f.concurrent.Add(1)
	defer f.concurrent.Add(-1)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.called++
	f.events = append(f.events, raw.Event)
	if f.disabled {
		return ErrDisabled
	}
	return f.fail
}

func sampleRaw(t *testing.T, event string) journal.Raw {
	t.Helper()
	line := []byte(`{"timestamp":"2026-05-21T12:00:00Z","event":"` + event + `"}`)
	raw, err := journal.ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMultiplex_DispatchesToAll(t *testing.T) {
	a := &fakeDest{name: "a"}
	b := &fakeDest{name: "b"}
	c := &fakeDest{name: "c"}
	m := NewMultiplex(a, b, c)

	if err := m.HandleEvent(context.Background(), sampleRaw(t, "FSDJump")); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	for _, d := range []*fakeDest{a, b, c} {
		if d.called != 1 {
			t.Errorf("dest %q called=%d, want 1", d.name, d.called)
		}
	}
}

func TestMultiplex_ContinuesAfterError(t *testing.T) {
	a := &fakeDest{name: "a", fail: errors.New("boom")}
	b := &fakeDest{name: "b"}
	m := NewMultiplex(a, b)

	if err := m.HandleEvent(context.Background(), sampleRaw(t, "FSDJump")); err != nil {
		t.Fatalf("HandleEvent should swallow per-destination errors; got %v", err)
	}
	if a.called != 1 || b.called != 1 {
		t.Errorf("error in first dest should not stop second: a=%d b=%d", a.called, b.called)
	}
}

func TestMultiplex_OnErrorCallback(t *testing.T) {
	a := &fakeDest{name: "a", fail: errors.New("boom-a")}
	b := &fakeDest{name: "b", fail: errors.New("boom-b")}
	m := NewMultiplex(a, b)
	var got []string
	m.OnError = func(name string, err error) {
		got = append(got, name+":"+err.Error())
	}
	if err := m.HandleEvent(context.Background(), sampleRaw(t, "FSDJump")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a:boom-a" || got[1] != "b:boom-b" {
		t.Errorf("OnError calls = %v", got)
	}
}

func TestMultiplex_DisabledIsNotAnError(t *testing.T) {
	a := &fakeDest{name: "a", disabled: true}
	b := &fakeDest{name: "b"}
	m := NewMultiplex(a, b)
	var onErrCalls int
	m.OnError = func(string, error) { onErrCalls++ }

	if err := m.HandleEvent(context.Background(), sampleRaw(t, "FSDJump")); err != nil {
		t.Fatal(err)
	}
	// The current Multiplex impl forwards ErrDisabled via OnError; in
	// practice the bridging code in web/server.go suppresses these. This
	// test pins the current behaviour so changes here are deliberate.
	if onErrCalls != 1 {
		t.Errorf("disabled dest should pass through OnError once; got %d", onErrCalls)
	}
	if b.called != 1 {
		t.Errorf("subsequent destinations must still be called when one returns ErrDisabled; b.called=%d", b.called)
	}
}

func TestMultiplex_Add(t *testing.T) {
	m := NewMultiplex()
	a := &fakeDest{name: "a"}
	m.Add(a)
	if err := m.HandleEvent(context.Background(), sampleRaw(t, "x")); err != nil {
		t.Fatal(err)
	}
	if a.called != 1 {
		t.Errorf("Add did not register dest")
	}

	// Add(nil) is a no-op.
	m.Add(nil)
	// Adding more destinations after dispatch picks them up next call.
	b := &fakeDest{name: "b"}
	m.Add(b)
	if err := m.HandleEvent(context.Background(), sampleRaw(t, "y")); err != nil {
		t.Fatal(err)
	}
	if b.called != 1 {
		t.Errorf("dest added mid-flight not called; b.called=%d", b.called)
	}
}

func TestMultiplex_Replace(t *testing.T) {
	a := &fakeDest{name: "a"}
	m := NewMultiplex(a)
	b := &fakeDest{name: "b"}
	m.Replace(b)
	if err := m.HandleEvent(context.Background(), sampleRaw(t, "x")); err != nil {
		t.Fatal(err)
	}
	if a.called != 0 {
		t.Errorf("Replace should have dropped a; a.called=%d", a.called)
	}
	if b.called != 1 {
		t.Errorf("b should have received the event; b.called=%d", b.called)
	}
}

func TestMultiplex_NilOrEmpty(t *testing.T) {
	m := &Multiplex{}
	if err := m.HandleEvent(context.Background(), sampleRaw(t, "x")); err != nil {
		t.Errorf("empty Multiplex should be a no-op; got %v", err)
	}
	m2 := NewMultiplex()
	if err := m2.HandleEvent(context.Background(), sampleRaw(t, "x")); err != nil {
		t.Errorf("NewMultiplex() with no dests should be a no-op; got %v", err)
	}
}

func TestMultiplex_ConcurrentDispatchAndReplace(t *testing.T) {
	// Hammer HandleEvent and Replace from many goroutines; the RWMutex
	// inside Multiplex should keep this race-free.
	m := NewMultiplex(&fakeDest{name: "a"})

	var wg sync.WaitGroup
	const N = 50
	wg.Add(2 * N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = m.HandleEvent(context.Background(), sampleRaw(t, "Tick"))
		}()
		go func() {
			defer wg.Done()
			m.Replace(&fakeDest{name: "x"}, &fakeDest{name: "y"})
		}()
	}
	wg.Wait()
}
