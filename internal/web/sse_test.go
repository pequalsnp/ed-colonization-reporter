package web

import (
	"sync"
	"testing"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

func TestStatusHub_MultipleSubscribersAllReceive(t *testing.T) {
	hub := newStatusHub()

	const subscribers = 5
	chs := make([]<-chan reporter.Status, subscribers)
	cancels := make([]func(), subscribers)
	for i := 0; i < subscribers; i++ {
		chs[i], cancels[i] = hub.Subscribe()
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	hub.Publish(reporter.Status{Level: reporter.LevelOK, Message: "fan-out"})

	var wg sync.WaitGroup
	wg.Add(subscribers)
	for i, ch := range chs {
		i, ch := i, ch
		go func() {
			defer wg.Done()
			select {
			case s := <-ch:
				if s.Message != "fan-out" {
					t.Errorf("subscriber %d got %q, want fan-out", i, s.Message)
				}
			case <-time.After(500 * time.Millisecond):
				t.Errorf("subscriber %d did not receive event", i)
			}
		}()
	}
	wg.Wait()
}

func TestStatusHub_CancelStopsReceiving(t *testing.T) {
	hub := newStatusHub()
	ch, cancel := hub.Subscribe()

	hub.Publish(reporter.Status{Message: "before-cancel"})
	// Drain the buffered backlog so the cancel later doesn't race with the
	// initial replay goroutine.
	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive initial event")
	}

	cancel()
	// Subscriber's chan should be closed promptly. Publish more — should not panic.
	hub.Publish(reporter.Status{Message: "after-cancel"})

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // expected closed channel
			}
			// Drain residual events that the replay goroutine may have pushed.
		case <-deadline:
			t.Fatal("subscriber channel was not closed after cancel")
		}
	}
}

func TestStatusHub_BufferCapped(t *testing.T) {
	hub := newStatusHub()
	for i := 0; i < 1000; i++ {
		hub.Publish(reporter.Status{Message: "x"})
	}
	hub.mu.Lock()
	bufLen := len(hub.buffer)
	hub.mu.Unlock()
	if bufLen > 200 {
		t.Errorf("buffer grew unbounded: len=%d", bufLen)
	}
}

func TestStatusHub_LateSubscriberGetsRecentBacklog(t *testing.T) {
	hub := newStatusHub()
	hub.Publish(reporter.Status{Message: "early-1"})
	hub.Publish(reporter.Status{Message: "early-2"})
	hub.Publish(reporter.Status{Message: "early-3"})

	ch, cancel := hub.Subscribe()
	defer cancel()

	got := []string{}
	deadline := time.After(500 * time.Millisecond)
	for len(got) < 3 {
		select {
		case s := <-ch:
			got = append(got, s.Message)
		case <-deadline:
			t.Fatalf("only got %d of 3: %v", len(got), got)
		}
	}
	want := []string{"early-1", "early-2", "early-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
