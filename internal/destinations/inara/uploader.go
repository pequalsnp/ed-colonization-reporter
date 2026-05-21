package inara

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// SoftwareID identifies our uploader in the Inara header.
type SoftwareID struct {
	Name    string
	Version string
}

// DefaultFlushInterval is how often the worker batches and posts collected
// events. EDMC uses 35s; we use 30s, well within Inara's tolerance.
const DefaultFlushInterval = 30 * time.Second

// Uploader is the Inara destination. Events are queued on HandleEvent and
// flushed by a background worker on a fixed interval to amortise rate-limit
// pressure.
type Uploader struct {
	Endpoint   string
	Software   SoftwareID
	Session    *state.Session
	HTTPClient *http.Client

	mu      sync.Mutex
	apiKey  string
	queue   []Event
	suppressDock bool

	enabled atomic.Bool

	OnStatus func(level, msg string)
}

// New builds an Inara uploader. Call StartBackground after enabling.
func New(software SoftwareID, sess *state.Session) *Uploader {
	return &Uploader{
		Endpoint:   Endpoint,
		Software:   software,
		Session:    sess,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Name implements destinations.Destination.
func (u *Uploader) Name() string { return "Inara" }

// SetEnabled toggles uploads.
func (u *Uploader) SetEnabled(b bool) { u.enabled.Store(b) }

// Enabled reports the current enable state.
func (u *Uploader) Enabled() bool { return u.enabled.Load() }

// SetAPIKey updates the Inara API key.
func (u *Uploader) SetAPIKey(k string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.apiKey = k
}

// HandleEvent transforms the journal event into Inara events (if any) and
// enqueues them for the next flush. The actual HTTP call happens in the
// background flusher started via StartBackground.
func (u *Uploader) HandleEvent(ctx context.Context, raw journal.Raw) error {
	if !u.enabled.Load() {
		return destinations.ErrDisabled
	}
	if u.Session.Commander() == "" {
		return nil
	}
	// Inara refuses Legacy/Beta data — drop on the floor if the game is
	// running against one of those galaxies.
	if gv, _ := u.Session.GameVersion(); isBetaOrLegacy(gv) {
		return nil
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	events, err := mapEvent(raw, &u.suppressDock)
	if err != nil {
		return fmt.Errorf("inara map %s: %w", raw.Event, err)
	}
	if len(events) == 0 {
		return nil
	}
	u.queue = append(u.queue, events...)
	return nil
}

// StartBackground spins up the periodic flusher. Returns immediately; the
// flusher runs until ctx is cancelled.
func (u *Uploader) StartBackground(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	go u.flushLoop(ctx, interval)
}

func (u *Uploader) flushLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// One last flush so we don't lose buffered events on shutdown.
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = u.Flush(flushCtx)
			cancel()
			return
		case <-t.C:
			if err := u.Flush(ctx); err != nil {
				u.status("ERROR", "Inara flush failed: "+err.Error())
			}
		}
	}
}

// Flush sends whatever events are currently queued. It is safe to call
// concurrently with HandleEvent.
func (u *Uploader) Flush(ctx context.Context) error {
	if !u.enabled.Load() {
		return nil
	}
	u.mu.Lock()
	key := u.apiKey
	if key == "" || len(u.queue) == 0 {
		u.mu.Unlock()
		return nil
	}
	events := u.queue
	u.queue = nil
	u.mu.Unlock()

	cmdr := u.Session.Commander()
	if cmdr == "" {
		return nil // shouldn't happen — enqueueing guards on this — but be safe
	}
	snap := u.Session.Snapshot()
	req := Request{
		Header: Header{
			AppName:             u.Software.Name,
			AppVersion:          u.Software.Version,
			APIKey:              key,
			CommanderName:       cmdr,
			CommanderFrontierID: snap.FID,
		},
		Events: events,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := u.HTTPClient.Do(httpReq)
	if err != nil {
		// Requeue events so they get another chance on the next tick.
		u.requeueFront(events)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		// 5xx and timeouts get a retry; 4xx is our fault, drop the batch.
		if resp.StatusCode >= 500 {
			u.requeueFront(events)
		}
		return fmt.Errorf("Inara HTTP %s: %s", resp.Status, snippet)
	}
	var reply Reply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return fmt.Errorf("decode reply: %w", err)
	}
	if reply.Header.EventStatus/100 != 2 {
		// Batch-level rejection (usually auth). Drop the batch — retrying
		// won't help. EDMC does the same.
		err := fmt.Errorf("Inara batch rejected (%d): %s", reply.Header.EventStatus, reply.Header.EventStatusText)
		u.status("ERROR", err.Error())
		return err
	}
	ok, warn, fail := 0, 0, 0
	for _, ev := range reply.Events {
		switch {
		case ev.EventStatus == 200:
			ok++
		case ev.EventStatus/100 == 2:
			warn++
		default:
			fail++
		}
	}
	u.status("OK", fmt.Sprintf("Inara: posted %d events (%d ok, %d warn, %d fail)", len(events), ok, warn, fail))
	return nil
}

// requeueFront puts events back at the front of the queue so the next flush
// retries them ahead of newly-arrived ones.
func (u *Uploader) requeueFront(events []Event) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.queue = append(events, u.queue...)
}

// QueueLen is exported for tests/observability. Not part of any interface.
func (u *Uploader) QueueLen() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.queue)
}

func (u *Uploader) status(level, msg string) {
	if u.OnStatus != nil {
		u.OnStatus(level, msg)
	}
}

// Sentinel for unexpected nil ctx in tests.
var _ = errors.New
