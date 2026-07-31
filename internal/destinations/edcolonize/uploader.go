package edcolonize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// Defaults for the push cadence. A snapshot is a complete picture, not a
// delta, so there is nothing to gain from per-event chatter — during a
// journal replay or a busy combat log the debounce collapses thousands of
// events into one POST per interval.
const (
	DefaultMinInterval = 5 * time.Second
	DefaultTimeout     = 5 * time.Second
)

// Config configures the edcolonize push target.
type Config struct {
	// Enabled gates the whole destination. When false, HandleEvent returns
	// destinations.ErrDisabled and does no work.
	Enabled bool
	// URL is the full snapshot endpoint, e.g.
	// http://172.16.3.208:3000/api/cmdr/snapshot
	URL string
	// Token is the shared secret; must match edcolonize's CMDR_INGEST_TOKEN.
	Token string
	// MinInterval is the debounce floor. Zero means DefaultMinInterval.
	MinInterval time.Duration
	// Timeout bounds a single POST. Zero means DefaultTimeout.
	Timeout time.Duration
}

// Uploader is the edcolonize destination. It watches journal events, keeps
// the parts of commander state that state.Session doesn't already track, and
// pushes a debounced snapshot to edcolonize.
//
// # Failure policy
//
// This optional integration must never degrade the reporting users actually
// depend on. A self-hosted edcolonize box may be rebooting, mid-deploy, or
// simply off; none of that is a reason to slow down or fail
// ravencolonial/EDDN/EDSM/Inara. So: HandleEvent never blocks on the network,
// POST failures are dropped rather than retried, and errors are surfaced once
// per outage rather than once per event.
//
// # Ordering dependency
//
// Register this AFTER reporter.Reporter in the multiplex. The Reporter is
// what populates state.Session; the multiplex dispatches in registration
// order, so registering earlier would snapshot the previous event's state.
type Uploader struct {
	cfg     Config
	sess    *state.Session
	client  *http.Client
	softwre SoftwareID

	// OnError is called at most once per outage (and once on recovery) so a
	// dead edcolonize doesn't flood the activity log. Optional.
	OnError func(error)

	mu       sync.Mutex
	tracked  trackedState
	dirty    bool
	lastPush time.Time
	timer    *time.Timer
	closed   bool

	// failing tracks whether we're inside an outage, so OnError fires on the
	// transition rather than on every attempt.
	failing bool

	// stats, for the GUI status surface and tests.
	pushOK   int
	pushFail int
}

// SoftwareID identifies this client to edcolonize. Unused on the wire today
// but kept symmetric with the other destinations' constructors.
type SoftwareID struct {
	Name    string
	Version string
}

// trackedState is the commander state that state.Session does NOT already
// hold. Session covers commander, system, docked, ship, cargo and carriers
// because the outward destinations need those; materials, missions, ranks,
// credits and construction depots are only interesting to edcolonize, so
// they live here rather than bloating the shared session type.
type trackedState struct {
	credits   *int64
	ranks     *Ranks
	materials *Materials
	missions  map[int64]Mission
	depots    map[int64]Depot
}

// New builds an edcolonize destination. A zero-value or disabled Config
// yields an Uploader that does nothing, which keeps the wiring in
// internal/web/server.go free of conditionals.
func New(software SoftwareID, cfg Config, sess *state.Session) *Uploader {
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = DefaultMinInterval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &Uploader{
		cfg:     cfg,
		sess:    sess,
		softwre: software,
		client:  &http.Client{Timeout: cfg.Timeout},
		tracked: trackedState{
			missions: make(map[int64]Mission),
			depots:   make(map[int64]Depot),
		},
	}
}

// Name implements destinations.Destination.
func (u *Uploader) Name() string { return "edcolonize" }

// Compile-time check.
var _ destinations.Destination = (*Uploader)(nil)

// HandleEvent implements destinations.Destination.
//
// Replayed events are deliberately NOT skipped. Unlike ColonisationContribution
// (which would re-attribute a delivery) a snapshot is idempotent whole-state,
// and the receiver upserts latest-wins — so replaying a journal just rebuilds
// the picture we want to send. The debounce keeps a replay to one POST per
// interval rather than thousands.
func (u *Uploader) HandleEvent(ctx context.Context, raw journal.Raw) error {
	if !u.cfg.Enabled || u.cfg.URL == "" {
		return destinations.ErrDisabled
	}

	interesting, err := u.absorb(raw)
	if err != nil {
		// A malformed payload for one event shouldn't stop the channel; the
		// next snapshot simply carries slightly older data for that section.
		return fmt.Errorf("edcolonize: %s: %w", raw.Event, err)
	}
	if !interesting {
		return nil
	}
	u.markDirty()
	return nil
}

// absorb folds one event into tracked state. Returns true when the event
// could plausibly have changed the snapshot, so a push is worth scheduling.
//
// Location/FSDJump/Docked/Undocked/LoadGame/Loadout and the carrier events
// all mutate state.Session (via reporter.Reporter, which runs before us), so
// we don't re-parse them — we just note that the snapshot moved.
func (u *Uploader) absorb(raw journal.Raw) (bool, error) {
	switch raw.Event {
	case "Location", "FSDJump", "CarrierJump", "Docked", "Undocked",
		"Loadout", "CarrierStats", "CarrierLocation", "Cargo":
		// Session already has these; nothing to track locally.
		return true, nil

	case "LoadGame":
		var e journal.LoadGameEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		u.mu.Lock()
		credits := e.Credits
		u.tracked.credits = &credits
		u.mu.Unlock()
		return true, nil

	case "Materials":
		var e materialsEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		m := &Materials{
			Raw:          toMaterialItems(e.Raw),
			Manufactured: toMaterialItems(e.Manufactured),
			Encoded:      toMaterialItems(e.Encoded),
		}
		u.mu.Lock()
		u.tracked.materials = m
		u.mu.Unlock()
		return true, nil

	case "MaterialCollected", "MaterialDiscarded", "MaterialTrade",
		"EngineerCraft", "Synthesis":
		// These shift material counts but the journal doesn't restate the
		// full inventory, so we can't apply an accurate delta without
		// duplicating the game's rules. Mark dirty so the snapshot's
		// timestamp stays honest; counts refresh at the next Materials event
		// (session start). Documented in the MCP tool's response note.
		return true, nil

	case "Rank":
		var e rankEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		u.mu.Lock()
		if u.tracked.ranks == nil {
			u.tracked.ranks = &Ranks{}
		}
		r := u.tracked.ranks
		r.Combat, r.Trade, r.Explore = e.Combat, e.Trade, e.Explore
		r.Soldier, r.Exobiologist, r.CQC = e.Soldier, e.Exobiologist, e.CQC
		r.Empire, r.Federation = e.Empire, e.Federation
		u.mu.Unlock()
		return true, nil

	case "Progress":
		var e rankEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		u.mu.Lock()
		if u.tracked.ranks == nil {
			u.tracked.ranks = &Ranks{}
		}
		r := u.tracked.ranks
		r.CombatProgress, r.TradeProgress, r.ExploreProgress = e.Combat, e.Trade, e.Explore
		r.EmpireProgress, r.FederationProgress = e.Empire, e.Federation
		u.mu.Unlock()
		return true, nil

	case "MissionAccepted":
		var e missionAcceptedEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		m := Mission{
			MissionID:          e.MissionID,
			Name:               e.Name,
			LocalisedName:      e.LocalisedName,
			Faction:            e.Faction,
			DestinationSystem:  e.DestinationSystem,
			DestinationStation: e.DestinationStation,
			Commodity:          firstNonEmpty(e.CommodityLocalised, e.Commodity),
			Count:              e.Count,
			Reward:             e.Reward,
			AcceptedAt:         raw.Timestamp,
		}
		if !e.Expiry.IsZero() {
			exp := e.Expiry
			m.Expiry = &exp
		}
		u.mu.Lock()
		u.tracked.missions[e.MissionID] = m
		u.mu.Unlock()
		return true, nil

	case "MissionCompleted", "MissionAbandoned", "MissionFailed":
		var e struct {
			MissionID int64 `json:"MissionID"`
		}
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		u.mu.Lock()
		delete(u.tracked.missions, e.MissionID)
		u.mu.Unlock()
		return true, nil

	case "Missions":
		// Emitted at session start with the authoritative active list. Use it
		// to drop anything we think is active but isn't — otherwise a mission
		// resolved while the reporter was closed would linger forever.
		var e missionsEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		active := make(map[int64]bool, len(e.Active))
		for _, a := range e.Active {
			active[a.MissionID] = true
		}
		u.mu.Lock()
		for id := range u.tracked.missions {
			if !active[id] {
				delete(u.tracked.missions, id)
			}
		}
		// Record any active mission we have no detail for, so the count is
		// right even before its MissionAccepted is seen.
		for _, a := range e.Active {
			if _, known := u.tracked.missions[a.MissionID]; !known {
				u.tracked.missions[a.MissionID] = Mission{
					MissionID:     a.MissionID,
					Name:          a.Name,
					LocalisedName: a.LocalisedName,
					Expiry:        nil,
					AcceptedAt:    raw.Timestamp,
				}
			}
		}
		u.mu.Unlock()
		return true, nil

	case "ColonisationConstructionDepot":
		var e journal.ColonisationConstructionDepotEvent
		if err := json.Unmarshal(raw.Payload, &e); err != nil {
			return false, err
		}
		d := Depot{
			MarketID:             e.MarketID,
			ConstructionProgress: e.ConstructionProgress,
			ConstructionComplete: e.ConstructionComplete,
			ConstructionFailed:   e.ConstructionFailed,
			UpdatedAt:            raw.Timestamp,
		}
		for _, r := range e.ResourcesRequired {
			d.Resources = append(d.Resources, DepotResource{
				Name:          strings.Trim(r.Name, "$;"),
				LocalisedName: r.NameLocalised,
				Required:      r.RequiredAmount,
				Provided:      r.ProvidedAmount,
				Payment:       int64(r.Payment),
			})
		}
		// The depot event carries only a MarketID. Enrich from the session:
		// the commander is necessarily at the site to receive it, so the
		// current system (and station, when docked) is the right attribution.
		sysName, sysAddr := u.sess.System()
		d.StarSystem, d.SystemAddress = sysName, sysAddr
		if docked, station, marketID := u.sess.Dock(); docked && marketID == e.MarketID {
			d.StationName = station
		}
		u.mu.Lock()
		u.tracked.depots[e.MarketID] = d
		u.mu.Unlock()
		return true, nil

	case "ColonisationContribution":
		// Contribution changes what a depot has been given; the game emits a
		// fresh ColonisationConstructionDepot right after, which carries the
		// authoritative totals. Just mark dirty.
		return true, nil
	}
	return false, nil
}

// markDirty records that state moved and schedules a push, respecting the
// debounce floor. Never blocks: the POST happens on a timer goroutine.
func (u *Uploader) markDirty() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	u.dirty = true
	if u.timer != nil {
		return // a flush is already scheduled
	}
	wait := time.Until(u.lastPush.Add(u.cfg.MinInterval))
	if wait < 0 {
		wait = 0
	}
	u.timer = time.AfterFunc(wait, u.flush)
}

// flush builds and sends one snapshot. Runs on the timer goroutine.
func (u *Uploader) flush() {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.timer = nil
	if !u.dirty {
		u.mu.Unlock()
		return
	}
	u.dirty = false
	u.lastPush = time.Now()
	snap := u.buildLocked()
	u.mu.Unlock()

	if snap == nil {
		return // nothing worth sending yet (no commander known)
	}
	u.post(snap)
}

// post sends one snapshot, dropping it on any failure.
func (u *Uploader) post(snap *Snapshot) {
	body, err := json.Marshal(snap)
	if err != nil {
		u.noteFailure(fmt.Errorf("marshal snapshot: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), u.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.cfg.URL, bytes.NewReader(body))
	if err != nil {
		u.noteFailure(fmt.Errorf("build request: %w", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.cfg.Token)
	req.Header.Set("User-Agent", u.softwre.Name+"/"+u.softwre.Version)

	resp, err := u.client.Do(req)
	if err != nil {
		// Unreachable host, DNS failure, timeout: expected whenever the
		// Unraid box is down. Drop and carry on.
		u.noteFailure(fmt.Errorf("post snapshot: %w", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		u.noteFailure(fmt.Errorf("edcolonize returned %s", resp.Status))
		return
	}
	u.noteSuccess()
}

func (u *Uploader) noteFailure(err error) {
	u.mu.Lock()
	u.pushFail++
	first := !u.failing
	u.failing = true
	onErr := u.OnError
	u.mu.Unlock()

	// Report the start of an outage once. Reporting every dropped snapshot
	// would bury the activity log during a reboot of the Unraid box.
	if first && onErr != nil {
		onErr(err)
	}
}

func (u *Uploader) noteSuccess() {
	u.mu.Lock()
	u.pushOK++
	recovered := u.failing
	u.failing = false
	onErr := u.OnError
	u.mu.Unlock()

	if recovered && onErr != nil {
		onErr(fmt.Errorf("edcolonize: snapshot push recovered"))
	}
}

// Stats reports push counters, for a status surface and for tests.
func (u *Uploader) Stats() (ok, failed int, failing bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.pushOK, u.pushFail, u.failing
}

// Close stops any pending flush. Safe to call more than once.
func (u *Uploader) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	if u.timer != nil {
		u.timer.Stop()
		u.timer = nil
	}
}

// Flush sends any pending snapshot immediately, bypassing the debounce. Used
// on shutdown so the last known state isn't lost, and by tests.
func (u *Uploader) Flush() {
	u.mu.Lock()
	if u.closed || !u.dirty {
		u.mu.Unlock()
		return
	}
	u.dirty = false
	u.lastPush = time.Now()
	if u.timer != nil {
		u.timer.Stop()
		u.timer = nil
	}
	snap := u.buildLocked()
	u.mu.Unlock()

	if snap != nil {
		u.post(snap)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func toMaterialItems(in []materialRow) []MaterialItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]MaterialItem, 0, len(in))
	for _, m := range in {
		out = append(out, MaterialItem{
			Name:          m.Name,
			LocalisedName: m.NameLocalised,
			Count:         m.Count,
		})
	}
	// Stable order keeps snapshots byte-comparable in tests and diffs.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
